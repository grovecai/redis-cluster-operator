#!/bin/bash

set -x
set -e

kubectl delete -f deploy/cluster/operator.yaml
kubectl delete -f deploy/cluster/cluster_role_binding.yaml
kubectl delete -f deploy/cluster/cluster_role.yaml
kubectl delete -f deploy/service_account.yaml

kubectl delete -f deploy/crds/redis.kun_redisclusterbackups_crd.yaml
kubectl delete -f deploy/crds/redis.kun_distributedredisclusters_crd.yaml

kubectl get deployment
