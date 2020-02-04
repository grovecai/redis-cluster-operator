#!/bin/bash
set -x
set -e
cd `dirname $0`
# 使用minikube的docker，避免手动传镜像 
eval $(minikube docker-env)
operator-sdk build redis-cluster-operator:latest
