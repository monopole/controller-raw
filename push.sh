#!/bin/bash

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

sed -i -E "s/(reboot-agent:).*/\1$GVERSION/" Examples/reboot-agent.yaml

echo ready for
echo kubectl apply -f Examples/reboot-agent.yaml