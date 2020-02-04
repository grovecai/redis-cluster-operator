#!/bin/bash
set -x
set -e

kubectl create -f deploy/example/redis-production.yaml
kubectl create -f deploy/example/redis-production-monitor-service.yaml
