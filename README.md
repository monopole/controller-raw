# Demo Controller

Fork of aaronlevy/kube-controller-demo


Goal is to be able to force the reboot of a node without ssh'ing into the node.

Instead one could use `kubectl` like this:

```
kubectl annotate --overwrite node \
    gke-chaos-default-pool-0db73b66-h772 \
    reboot-agent.v1.demo.local/reboot-now=yes
```

To see what happens, scan the log of the pod on that node that corresponds
to the controller:

```
kubectl logs reboot-agent-6grdw
```

## Two controllers

### reboot-agent

This is a controller.  It's expressed as an image held
by a daemonSet, to force installation of one replica on each pod.

As nodes added to cluster, a pod with the controller
is automatically added too.

All it does is watch for a `reboot-now` annotation on a node -
when it sees it, it removes the anno and adds the anno
`reboot-in-progress`, then
starts the reboot (or merely sleeps, simulating a reboot).

### reboot-controller

This is a
A Deployment with numreplicas == 1, it watches for a node with the annotation
`reboot-needed` (applied by a human).  It counts unavailable nodes, and has
a loop

> ```
> while numUnvailable < maxUnavailable :
>   on a node, replace reboot-needed with reboot-now
> ```

So this controller is merely a guard against rebooting
too many things at once.

## Required env vars

PERSONAL
```
PROJECT_ID=lyrical-gantry-618
CLUSTER_NAME=yadayada
```

WORK
```
PROJECT_ID=kustomize-199618
CLUSTER_NAME=yaksdee-colo-1
```


## Do this to set context:

```
gcloud auth application-default login
```

```
gcloud config set project $PROJECT_ID
gcloud config list

```

## Delete a cluster

```
gcloud --quiet container clusters \
  delete $CLUSTER_NAME \
  --zone "us-west1-a"
```

## Create a cluster

```
# gcloud config set compute/region us-west1
# gcloud config set compute/zone us-west1-a
```

```
scopes=\
"https://www.googleapis.com/auth/devstorage.read_only",\
"https://www.googleapis.com/auth/logging.write",\
"https://www.googleapis.com/auth/monitoring",\
"https://www.googleapis.com/auth/servicecontrol",\
"https://www.googleapis.com/auth/service.management.readonly",\
"https://www.googleapis.com/auth/trace.append" 

clear
gcloud beta container \
  --project $PROJECT_ID \
  clusters create $CLUSTER_NAME \
  --zone "us-west1-a" \
  --num-nodes "6" \
  --username "admin" \
  --cluster-version "1.10.6-gke.2" \
  --machine-type "n1-standard-1" \
  --image-type "COS" \
  --disk-type "pd-standard" \
  --disk-size "100" \
  --scopes $scopes \
  --preemptible \
  --enable-cloud-logging \
  --enable-cloud-monitoring \
  --network "projects/${PROJECT_ID}/global/networks/default" \
  --subnetwork "projects/${PROJECT_ID}/regions/us-west1/subnetworks/default" \
  --addons HorizontalPodAutoscaling,HttpLoadBalancing \
  --no-enable-autoupgrade \
  --no-enable-autorepair
```

```
gcloud config list
```


## Set up RBAC

Update your `.kube/config` file with credentials:
```
gcloud container clusters get-credentials $CLUSTER_NAME \
  --zone us-west1-a 
```


Assure that you have cluster-admin privileges in your own cluster.

Is this needed?

```
export ACCOUNT=$(gcloud info --format='value(config.account)')
echo "ACCOUNT=$ACCOUNT"

kubectl create clusterrolebinding owner-cluster-admin-binding \
    --clusterrole cluster-admin --user $ACCOUNT
# See what you just did:
kubectl describe  clusterrolebindings owner-cluster-admin-binding
```


Create a service account for the controllers:
```
kubectl create serviceaccount blah-reboot-account

# See what you made
kubectl get serviceaccount blah-reboot-account -o yaml

kubectl create clusterrolebinding binding-reboot-agent-name \
  --clusterrole=cluster-admin \
  --serviceaccount=default:blah-reboot-account
  
kubectl get clusterrolebinding binding-reboot-agent-name -o yaml

```

After that, you must add 
```
      serviceAccountName: blah-reboot-account
```
to the `template/spec`, at the same indentation as `containers`,
or the calls to list Nodes will fail.

##  Coding prep

Set up the repos correctly:
```
myGitRebaseAll

# Maybe
# echo "Wiping the vendor in kube-controller-demo"
# cd $GOPATH/src/github.com/monopole/kube-controller-demo
# /bin/rm -rf vendor

# go get github.com/coreos/go-systemd/login1

myGitClientGo

```

Have not set up google cloud builder yet, so just log into docker

```
# Enter password followed by CTRL-D
docker login --username=monopole --password-stdin
```

Ideally commit all changes to get a non-dirty version:
```
git commit -a -m whatever
```

Run this script to build and push the images to docker hub
```
./push.sh
```

## Test the binary

With the binaries built, this command should work:
```
NODE_NAME=foo bin/reboot-agent --kubeconfig ~/.kube/config 
```

Can also try running in a docker context:
```
docker images # to see the gittag
docker run -d reboot-agent:{gittag}
docker ps
docker logs {containerID}
docker kill {containerId}
```

## Run it in cluster

```
kubectl apply -f Examples/reboot-agent.yaml
kubectl describe daemonset reboot-agent
kubectl get pods

# Pick any of the pods, dump its log:
pod=reboot-agent-2ct7q

kubectl logs $pod

# In the log you see it prints its own node name,
# which should match what you see this way:
kubectl describe pod $pod | grep Node:

# Set the node
node=gke-yaksdee-colo-1-default-pool-8b04a446-rb82

```

## Force a reboot directly at the agent

```

kubectl annotate --overwrite node $node \
    reboot-agent.v1.demo.local/reboot-now=yes


kubectl describe node $node | grep -C 1 Annotations:
kubectl logs $pod
```

## Send the big controller out - its just a Deployment:

```
kubectl apply -f Examples/reboot-controller.yaml
kubectl get pods
```

Tell it to reboot a node:
```
kubectl describe node $node | grep -C 1 Annotations:

kubectl annotate --overwrite node $node \
    reboot-agent.v1.demo.local/reboot-requested=yes

kubectl describe node $node | grep -C 1 Annotations:

kubectl logs $pod
```


Delete it:
```
kubectl delete daemonset reboot-agent; \
  kubectl delete deployment reboot-controller
```


##  Resources

- Upstream controller development and design principles
  - https://github.com/kubernetes/community/blob/master/contributors/devel/controllers.md
  - https://contributor.kubernetes.io/contributors/devel/controllers
  - https://github.com/kubernetes/community/blob/master/contributors/design-proposals/principles.md#control-logic

- Upstream Kubernetes controller package
  - https://github.com/kubernetes/kubernetes/tree/master/pkg/controller
  - https://github.com/kubernetes/kubernetes/tree/release-1.6/pkg/controller

- client-go examples (version sensitive, e.g. use v3 examples with v3 checkout)
  - https://github.com/kubernetes/client-go/tree/master/examples
  - https://github.com/kubernetes/client-go/tree/v3.0.0-beta.0/examples

- bitnami
  - https://engineering.bitnami.com/articles/a-deep-dive-into-kubernetes-controllers.html
  - https://engineering.bitnami.com/articles/kubewatch-an-example-of-kubernetes-custom-controller.html

- Creating Kubernetes Operators Presentation (@metral)
  - http://bit.ly/lax-k8s-operator

- Memcached operator written in python (@pst)
  - https://github.com/kbst/memcached

## Roadmap

- Demonstrate using
    - leader-election
    - Third Party Resources
    - Shared Informers
    - Events

## Building

Build agent and controller binaries:

`make clean all`

Build agent and controller Docker images:

`make clean images`

