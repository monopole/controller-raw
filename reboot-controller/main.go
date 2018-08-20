package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/monopole/kube-controller-demo/common"
	"k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	lister_v1 "k8s.io/client-go/listers/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// TODO(aaron): make configurable and add MinAvailable
const maxUnavailable = 1

func main() {
	// When running as a pod in-cluster, a kubeconfig is not needed. Instead this will make use of the service account injected into the pod.
	// However, allow the use of a local kubeconfig as this can make local development & testing easier.
	kubeconfig := flag.String("kubeconfig", "", "Path to a kubeconfig file")

	flag.Parse()
	log.Println("Controller Version 1.0")

	// Build the client config - optionally using a provided kubeconfig file.
	config, err := common.GetClientConfig(*kubeconfig)
	if err != nil {
		log.Panicf("Failed to load client config: %v", err)
	}

	// Construct the Kubernetes client
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Panicf("Failed to create kubernetes client: %v", err)
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	newRebootController(client).Run(stopCh)
}

type rebootController struct {
	client     kubernetes.Interface
	queue      workqueue.RateLimitingInterface
	informer   cache.Controller
	nodeLister lister_v1.NodeLister
}

func newRebootController(client kubernetes.Interface) *rebootController {
	rc := &rebootController{
		client: client,
		queue:  workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
	}

	indexer, informer := cache.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc: func(lo meta_v1.ListOptions) (runtime.Object, error) {
				// We do not add any selectors because we want to watch all nodes.
				// This is so we can determine the total count of "unavailable" nodes.
				// However, this could also be implemented using multiple informers (or better, shared-informers)
				return client.CoreV1().Nodes().List(lo)
			},
			WatchFunc: func(lo meta_v1.ListOptions) (watch.Interface, error) {
				return client.CoreV1().Nodes().Watch(lo)
			},
		},
		// The types of objects this informer will return
		&v1.Node{},
		// The resync period of this object. This will force a re-queue of all cached objects at this interval.
		// Every object will trigger the `Updatefunc` even if there have been no actual updates triggered.
		// In some cases you can set this to a very high interval - as you can assume you will see periodic
		// updates in normal operation.
		// The interval is set low here for demo purposes.
		10*time.Second,
		// Callback Functions to trigger on add/update/delete
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				if key, err := cache.MetaNamespaceKeyFunc(obj); err == nil {
					rc.queue.Add(key)
				}
			},
			UpdateFunc: func(old, new interface{}) {
				if key, err := cache.MetaNamespaceKeyFunc(new); err == nil {
					rc.queue.Add(key)
				}
			},
			DeleteFunc: func(obj interface{}) {
				if key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err == nil {
					rc.queue.Add(key)
				}
			},
		},
		cache.Indexers{},
	)

	rc.informer = informer
	// NodeLister avoids some boilerplate code (e.g. convert runtime.Object to *v1.node)
	rc.nodeLister = lister_v1.NewNodeLister(indexer)

	return rc
}

func (c *rebootController) Run(stopCh chan struct{}) {
	defer c.queue.ShutDown()
	log.Println("Starting RebootController")

	go c.informer.Run(stopCh)

	// Wait for all caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		log.Println("Timed out waiting for caches to sync")
		return
	}

	// Launching additional goroutines would parallelize workers consuming
	// from the queue (but we don't really need this)
	// The following runs the runWorker function once every second.
	// Each run is an infinite loop of pulling stuff of the Q, so this
	// makes little sense to me.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
	log.Print("Stopping Reboot Controller")
}

func (c *rebootController) runWorker() {
	for c.processNext() {
	}
}

func (c *rebootController) processNext() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.queue.Get()
	log.Printf("Pulled item from queue: %v", key)
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key.
	// This unblocks the key for other workers
	// This allows safe parallel processing because two pods with
	// the same key are never processed in parallel.
	defer c.queue.Done(key)
	// Invoke the method containing the business logic
	err := c.process(key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

func (c *rebootController) process(key string) error {
	node, err := c.nodeLister.Get(key)
	if err != nil {
		return fmt.Errorf("failed to retrieve node by key %q: %v", key, err)
	}

	log.Printf("Received update of node: %s\n", node.GetName())
	if node.Annotations == nil {
		return nil // If node has no annotations, then it doesn't need a reboot
	}

	if _, ok := node.Annotations[common.AnnoRebootRequested]; !ok {
		return nil // Node does not need reboot
	}

	// Determine if we should reboot based on maximum number of unavailable nodes
	unavailable, err := c.unavailableNodeCount()
	if err != nil {
		return fmt.Errorf("Failed to determine number of unavailable nodes: %v", err)
	}

	if unavailable >= maxUnavailable {
		// TODO(aaron): We might want this case to retry indefinitely.
		// Could create a specific error an check in handleErr()
		return fmt.Errorf(
			"Too many nodes unvailable (%d/%d). Skipping reboot of %s",
			unavailable, maxUnavailable, node.Name)
	}

	// We should not modify the cache object directly, so we make a copy first
	nodeCopy, err := common.CopyObjToNode(node)
	if err != nil {
		return fmt.Errorf("Failed to make copy of node: %v", err)
	}

	log.Printf("Marking node %s for reboot\n", node.Name)
	nodeCopy.Annotations[common.AnnoRebootNow] = ""
	if _, err := c.client.CoreV1().Nodes().Update(nodeCopy); err != nil {
		return fmt.Errorf("Failed to set %s annotation: %v", common.AnnoRebootNow, err)
	}
	return nil
}

func (c *rebootController) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		log.Printf("Error processing node %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	log.Printf("Dropping node %q out of the queue: %v\n", key, err)
}

func (c *rebootController) unavailableNodeCount() (int, error) {
	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		return 0, err
	}
	var unavailable int
	for _, n := range nodes {
		if nodeIsRebooting(n) {
			unavailable++
			continue
		}
		for _, c := range n.Status.Conditions {
			if c.Type == v1.NodeReady && c.Status == v1.ConditionFalse {
				unavailable++
			}
		}
	}
	return unavailable, nil
}

func nodeIsRebooting(n *v1.Node) bool {
	// Check if node is marked for reeboot-in-progress
	if n.Annotations == nil {
		return false // No annotations - not marked as needing reboot
	}
	if _, ok := n.Annotations[common.AnnoRebootInProgress]; ok {
		return true
	}
	// Check if node is already marked for immediate reboot
	_, ok := n.Annotations[common.AnnoRebootNow]
	return ok
}
