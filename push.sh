#!/bin/bash

function whatImages {
  for image in $(kubectl get pods --all-namespaces\
     --output=jsonpath='{..image}'); do
     echo $image
  done
}

make clean
make images

function pushToDockerHub {
  local dockerUser=monopole
  local pgmName=$1
  local gitTag=$2
  local repoName=$pgmName
  local id=$(docker images | grep -m 1 $pgmName | awk '{printf $3}')
  echo docker tag $id $dockerUser/$repoName:$gitTag
  docker tag $id $dockerUser/$repoName:$gitTag
  docker push $dockerUser/$repoName:$gitTag
}

function pushAllToHub {
  pushToDockerHub reboot-controller $1
  pushToDockerHub reboot-agent $1
}

GVERSION=`./git-version.sh`

pushAllToHub $GVERSION

sed -i -E "s|(monopole/reboot-agent:).*|\1$GVERSION|" Examples/reboot-agent.yaml
sed -i -E "s|(monopole/reboot-controller:).*|\1$GVERSION|" Examples/reboot-controller.yaml

echo " "
cat Examples/reboot-agent.yaml
echo " "
echo "Ready to: kubectl apply -f Examples/reboot-agent.yaml"


