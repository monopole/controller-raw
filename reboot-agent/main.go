package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/coreos/go-systemd/login1"
	"github.com/monopole/kube-controller-demo/common"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/tools/cache"
)

const nodeNameEnv = "NODE_NAME"

func main() {
	// When running as a pod in-cluster, a kubeconfig is not needed. Instead this will make use of the service account injected into the pod.
	// However, allow the use of a local kubeconfig as this can make local development & testing easier.
	kubeconfig := flag.String("kubeconfig", "", "Path to a kubeconfig file")

	flag.Parse()
	log.Println("Agent Version 1.0")

	// The node name is necessary so we can identify "self".
	// This environment variable is assumed to be set via the pod downward-api, however it can be manually set during testing
	nodeName := os.Getenv(nodeNameEnv)
	if nodeName == "" {
		log.Fatalf("Missing required environment variable %s", nodeNameEnv)
	}

	// Build the client config - optionally using a provided kubeconfig file.
	config, err := common.GetClientConfig(*kubeconfig)
	if err != nil {
		log.Fatalf("Failed to load client config: %v", err)
	}

	// Construct the Kubernetes client
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create kubernetes client: %v", err)
	}

	// Open a dbus connection for triggering a system reboot
	dbusConn, err := login1.New()
	if err != nil {
		log.Fatalf("Failed to create dbus connection")
	}

	agent := newRebootAgent(nodeName, client, dbusConn)

	// We immediately start processing events even if our cache might not have fully synced.
	// In this case this is safe because we only care about a single object - our own node.
	// If we get an event for "self" that is the only state we need, and no further cache syncing
	// is required. If we do start caring about cache state, we should implement a workqueue
	// and wait to process queue until cached has synced. See reboot-controller for example.
	log.Println("Starting Reboot Agent")
	agent.controller.Run(wait.NeverStop)
}

type rebootAgent struct {
	client     kubernetes.Interface
	dbusConn   *login1.Conn
	controller cache.Controller
}

func newRebootAgent(nodeName string, client kubernetes.Interface, dbusConn *login1.Conn) *rebootAgent {
	agent := &rebootAgent{
		client:   client,
		dbusConn: dbusConn,
	}

	// We only care about updates to "self" so create a field selector
	// based on the current node name
	nodeNameFS := fields.OneTermEqualSelector("metadata.name", nodeName).String()

	// We do not need the cache store of the informer.
	// In this case we just want the controller event handlers.
	_, controller := cache.NewInformer(
		&cache.ListWatch{
			// Knows how to list resources.
			ListFunc: func(lo metav1.ListOptions) (runtime.Object, error) {
				// Add the field selector containgin our node name to our list options
				lo.FieldSelector = nodeNameFS
				return client.CoreV1().Nodes().List(lo)
			},
			// Knows how to watch resources.
			WatchFunc: func(lo metav1.ListOptions) (watch.Interface, error) {
				// Add the field selector containing our node name to our list options
				lo.FieldSelector = nodeNameFS
				return client.CoreV1().Nodes().Watch(lo)
			},
		},
		// The types of objects this informer will return
		&v1.Node{},
		// The resync period of this object. This will force a re-queue of all cached objects at this interval.
		// Every object will trigger the `Updatefunc` even if there have been no actual updates triggered.
		// In some cases you can set this to a very high interval - as you can assume you will see periodic updates in normal operation.
		// The interval is set low here for demo purposes.
		10*time.Second,
		// Callback Functions to trigger on add/update/delete
		cache.ResourceEventHandlerFuncs{
			AddFunc:    agent.handleAdd,
			DeleteFunc: agent.handleDelete,
			UpdateFunc: agent.handleUpdate,
			// DeleteFunc: func(obj interface{}) {}
		},
	)

	agent.controller = controller

	return agent
}

func (a *rebootAgent) handleAdd(obj interface{}) {
	node, err := common.CopyObjToNode(obj)
	if err != nil {
		log.Printf("Failed to copy Node object in handleAdd: %v", err)
		return
	}
	log.Printf("Received add for node: %s", node.Name)
}

func (a *rebootAgent) handleDelete(obj interface{}) {
	node, err := common.CopyObjToNode(obj)
	if err != nil {
		log.Printf("Failed to copy Node object in handleDelete: %v", err)
		return
	}
	log.Printf("Received delete for node: %s", node.Name)
}

func (a *rebootAgent) handleUpdate(oldObj, newObj interface{}) {
	// In an `UpdateFunc` handler, before doing any work, you might try and determine if there has
	// ben an actual change between the oldObj and newObj.
	// This could mean checking the `resourceVersion` of the objects, and if they are the same -
	// there has been no change to the object.
	// Or it might mean only inspecting fields that you care about (as seen below).
	// However, you should be careful when ignoring updates to objects, as it is possible that prior update was missed,
	// and if you continue to ignore the objects, you will never fully sync desired state.

	// Because we are about to make changes to the object - we make a copy.
	// You should never mutate the original objects (from the cache.Store) as you are modifying state that has
	// not been persisted via the apiserver.
	// For example, if you modify the original object, but then your `Update()` call fails - your local cache
	// could now be "wrong".
	// Additionally, if using SharedInformers - you are modifying a local cache that could be used by other controllers.
	node, err := common.CopyObjToNode(newObj)
	if err != nil {
		log.Printf("Failed to copy Node object: %v", err)
		return
	}
	log.Printf("Received update for node: %s", node.Name)

	if shouldReboot(node) && !rebootInProgress(node) {
		log.Println("Reboot requested...")

		// Set RebootInProgress
		node.Annotations[common.AnnoRebootInProgress] = "yeah baby"
		delete(node.Annotations, common.AnnoRebootRequested)
		delete(node.Annotations, common.AnnoRebootNow)

		_, err := a.client.CoreV1().Nodes().Update(node)
		if err != nil {
			log.Printf("Failed to set %s annotation: %v", common.AnnoRebootInProgress, err)
			return // If we cannot update the state - do not reboot
		}

		// TODO(aaron): We should drain the node (this is really just for demo purposes - but would be good to demonstrate)

		log.Println("Would call reboot here... sleeping for 10")
		time.Sleep(10 * time.Second)
		log.Println("Waking.")
		return
		// a.dbusConn.Reboot(false)
		// select {} // Wait for machine to reboot
	}

	if rebootInProgress(node) {
		// Assume the node just rebooted.  Clear RebootInProgress
		log.Println("Clearing in-progress reboot annotation")
		delete(node.Annotations, common.AnnoRebootInProgress)

		_, err := a.client.CoreV1().Nodes().Update(node)
		if err != nil {
			log.Printf("Failed to remove %s annotation: %v", common.AnnoRebootInProgress, err)
			return
		}
	}
}

func shouldReboot(node *v1.Node) bool {
	_, reboot := node.Annotations[common.AnnoRebootNow]
	return reboot
}

func rebootInProgress(node *v1.Node) bool {
	_, inProgress := node.Annotations[common.AnnoRebootInProgress]
	return inProgress
}
