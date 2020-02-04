#!/bin/bash
set -x
set -e

kubectl delete -f deploy/example/redis-production.yaml
kubectl delete -f deploy/example/redis-production-monitor-service.yaml
