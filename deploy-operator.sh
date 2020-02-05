#! /bin/bash
set -x
set -e

kubectl create -f deploy/crds/redis.kun_distributedredisclusters_crd.yaml
kubectl create -f deploy/crds/redis.kun_redisclusterbackups_crd.yaml

kubectl create -f deploy/service_account.yaml
kubectl create -f deploy/cluster/cluster_role.yaml
kubectl create -f deploy/cluster/cluster_role_binding.yaml
kubectl create -f deploy/cluster/operator.yaml

kubectl create -f deploy/example/redis-production-log-configmap.yaml

kubectl get deployment
