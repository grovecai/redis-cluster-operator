package manager

import (
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redisv1alpha1 "github.com/ucloud/redis-cluster-operator/pkg/apis/redis/v1alpha1"
	"github.com/ucloud/redis-cluster-operator/pkg/k8sutil"
	"github.com/ucloud/redis-cluster-operator/pkg/osm"
	"github.com/ucloud/redis-cluster-operator/pkg/resources/configmaps"
	"github.com/ucloud/redis-cluster-operator/pkg/resources/poddisruptionbudgets"
	"github.com/ucloud/redis-cluster-operator/pkg/resources/services"
	"github.com/ucloud/redis-cluster-operator/pkg/resources/statefulsets"
)

type IEnsureResource interface {
	EnsureRedisStatefulsets(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) (bool, error)
	EnsureRedisHeadLessSvcs(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) error
	EnsureRedisSvc(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) error
	EnsureRedisConfigMap(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) error
	EnsureRedisOSMSecret(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) error
}

type realEnsureResource struct {
	statefulSetClient k8sutil.IStatefulSetControl
	svcClient         k8sutil.IServiceControl
	configMapClient   k8sutil.IConfigMapControl
	pdbClient         k8sutil.IPodDisruptionBudgetControl
	crClient          k8sutil.ICustomResource
	client            client.Client
	logger            logr.Logger
}

func NewEnsureResource(client client.Client, logger logr.Logger) IEnsureResource {
	return &realEnsureResource{
		statefulSetClient: k8sutil.NewStatefulSetController(client),
		svcClient:         k8sutil.NewServiceController(client),
		configMapClient:   k8sutil.NewConfigMapController(client),
		pdbClient:         k8sutil.NewPodDisruptionBudgetController(client),
		crClient:          k8sutil.NewCRControl(client),
		client:            client,
		logger:            logger,
	}
}

func (r *realEnsureResource) EnsureRedisStatefulsets(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) (bool, error) {
	updated := false
	for i := 0; i < int(cluster.Spec.MasterSize); i++ {
		name := statefulsets.ClusterStatefulSetName(cluster.Name, i)
		svcName := statefulsets.ClusterHeadlessSvcName(cluster.Spec.ServiceName, i)
		// assign label
		labels[redisv1alpha1.StatefulSetLabel] = name
		//FIXME: add labels for monitoring
		labels["app.kubernetes.io/component"] = fmt.Sprintf("shard-%d", i)

		if stsUpdated, err := r.ensureRedisStatefulset(cluster, name, svcName, labels); err != nil {
			return false, err
		} else if stsUpdated {
			updated = stsUpdated
		}
	}
	return updated, nil
}

func (r *realEnsureResource) ensureRedisStatefulset(cluster *redisv1alpha1.DistributedRedisCluster, ssName, svcName string,
	labels map[string]string) (bool, error) {
	if err := r.ensureRedisPDB(cluster, ssName, labels); err != nil {
		return false, err
	}

	ss, err := r.statefulSetClient.GetStatefulSet(cluster.Namespace, ssName)
	if err == nil {
		if shouldUpdateRedis(cluster, ss) {
			r.logger.WithValues("StatefulSet.Namespace", cluster.Namespace, "StatefulSet.Name", ssName).
				Info("updating statefulSet")
			newSS, err := statefulsets.NewStatefulSetForCR(cluster, ssName, svcName, labels)
			if err != nil {
				return false, err
			}
			return true, r.statefulSetClient.UpdateStatefulSet(newSS)
		}
	} else if err != nil && errors.IsNotFound(err) {
		r.logger.WithValues("StatefulSet.Namespace", cluster.Namespace, "StatefulSet.Name", ssName).
			Info("creating a new statefulSet")
		newSS, err := statefulsets.NewStatefulSetForCR(cluster, ssName, svcName, labels)
		if err != nil {
			return false, err
		}
		return false, r.statefulSetClient.CreateStatefulSet(newSS)
	}
	return false, err
}

func shouldUpdateRedis(cluster *redisv1alpha1.DistributedRedisCluster, sts *appsv1.StatefulSet) bool {
	if (cluster.Spec.ClusterReplicas + 1) != *sts.Spec.Replicas {
		return true
	}
	if cluster.Spec.Image != sts.Spec.Template.Spec.Containers[0].Image {
		return true
	}
	if cluster.Spec.PasswordSecret != nil {
		envSet := sts.Spec.Template.Spec.Containers[0].Env
		secretName := getSecretKeyRefByKey(redisv1alpha1.PasswordENV, envSet)
		if secretName == "" {
			return true
		}
		if secretName != cluster.Spec.PasswordSecret.Name {
			return true
		}
	}

	expectResource := cluster.Spec.Resources
	currentResource := sts.Spec.Template.Spec.Containers[0].Resources
	if result := expectResource.Requests.Memory().Cmp(*currentResource.Requests.Memory()); result != 0 {
		return true
	}
	if result := expectResource.Requests.Cpu().Cmp(*currentResource.Requests.Cpu()); result != 0 {
		return true
	}
	if result := expectResource.Limits.Memory().Cmp(*currentResource.Limits.Memory()); result != 0 {
		return true
	}
	if result := expectResource.Limits.Cpu().Cmp(*currentResource.Limits.Cpu()); result != 0 {
		return true
	}
	return false
}

func getSecretKeyRefByKey(key string, envSet []corev1.EnvVar) string {
	for _, value := range envSet {
		if key == value.Name {
			if value.ValueFrom != nil && value.ValueFrom.SecretKeyRef != nil {
				return value.ValueFrom.SecretKeyRef.Name
			}
		}
	}
	return ""
}

func (r *realEnsureResource) ensureRedisPDB(cluster *redisv1alpha1.DistributedRedisCluster, name string, labels map[string]string) error {
	_, err := r.pdbClient.GetPodDisruptionBudget(cluster.Namespace, name)
	if err != nil && errors.IsNotFound(err) {
		r.logger.WithValues("PDB.Namespace", cluster.Namespace, "PDB.Name", name).
			Info("creating a new PodDisruptionBudget")
		pdb := poddisruptionbudgets.NewPodDisruptionBudgetForCR(cluster, name, labels)
		return r.pdbClient.CreatePodDisruptionBudget(pdb)
	}
	return err
}

func (r *realEnsureResource) EnsureRedisHeadLessSvcs(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) error {
	for i := 0; i < int(cluster.Spec.MasterSize); i++ {
		svcName := statefulsets.ClusterHeadlessSvcName(cluster.Spec.ServiceName, i)
		name := statefulsets.ClusterStatefulSetName(cluster.Name, i)
		// assign label
		labels[redisv1alpha1.StatefulSetLabel] = name
		//FIXME: add labels for monitoring
		labels["app.kubernetes.io/component"] = fmt.Sprintf("shard-%d", i)
		if err := r.ensureRedisHeadLessSvc(cluster, svcName, labels); err != nil {
			return err
		}
	}
	return nil
}

func (r *realEnsureResource) ensureRedisHeadLessSvc(cluster *redisv1alpha1.DistributedRedisCluster, name string, labels map[string]string) error {
	_, err := r.svcClient.GetService(cluster.Namespace, name)
	if err != nil && errors.IsNotFound(err) {
		r.logger.WithValues("Service.Namespace", cluster.Namespace, "Service.Name", cluster.Spec.ServiceName).
			Info("creating a new headless service")
		svc := services.NewHeadLessSvcForCR(cluster, name, labels)
		return r.svcClient.CreateService(svc)
	}
	return err
}

func (r *realEnsureResource) EnsureRedisSvc(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) error {
	name := cluster.Spec.ServiceName
	delete(labels, redisv1alpha1.StatefulSetLabel)
	//FIXME: for monitoring
	delete(labels, "app.kubernetes.io/component")
	_, err := r.svcClient.GetService(cluster.Namespace, name)
	if err != nil && errors.IsNotFound(err) {
		r.logger.WithValues("Service.Namespace", cluster.Namespace, "Service.Name", cluster.Spec.ServiceName).
			Info("creating a new service")
		svc := services.NewSvcForCR(cluster, name, labels)
		return r.svcClient.CreateService(svc)
	}
	return err
}

func (r *realEnsureResource) EnsureRedisConfigMap(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) error {
	cmName := configmaps.RedisConfigMapName(cluster.Name)
	_, err := r.configMapClient.GetConfigMap(cluster.Namespace, cmName)
	if err != nil {
		if errors.IsNotFound(err) {
			r.logger.WithValues("ConfigMap.Namespace", cluster.Namespace, "ConfigMap.Name", cmName).
				Info("creating a new configMap")
			cm := configmaps.NewConfigMapForCR(cluster, labels)
			if err2 := r.configMapClient.CreateConfigMap(cm); err2 != nil {
				return err2
			}
		} else {
			return err
		}
	}

	if cluster.IsRestoreFromBackup() {
		restoreCmName := configmaps.RestoreConfigMapName(cluster.Name)
		restoreCm, err := r.configMapClient.GetConfigMap(cluster.Namespace, restoreCmName)
		if err != nil {
			if errors.IsNotFound(err) {
				r.logger.WithValues("ConfigMap.Namespace", cluster.Namespace, "ConfigMap.Name", restoreCmName).
					Info("creating a new restore configMap")
				cm := configmaps.NewConfigMapForRestore(cluster, labels)
				return r.configMapClient.CreateConfigMap(cm)
			}
			return err
		}
		if restoreCm.Data[configmaps.RestoreSucceeded] != strconv.Itoa(int(cluster.Status.Restore.RestoreSucceeded)) {
			cm := configmaps.NewConfigMapForRestore(cluster, labels)
			return r.configMapClient.UpdateConfigMap(cm)
		}
	}
	return nil
}

func (r *realEnsureResource) EnsureRedisOSMSecret(cluster *redisv1alpha1.DistributedRedisCluster, labels map[string]string) error {
	if !cluster.IsRestoreFromBackup() || cluster.IsRestored() {
		return nil
	}
	backup := cluster.Status.Restore.Backup
	secret, err := osm.NewCephSecret(r.client, backup.OSMSecretName(), cluster.Namespace, backup.Spec.Backend)
	if err != nil {
		return err
	}
	if err := k8sutil.CreateSecret(r.client, secret, r.logger); err != nil {
		return err
	}
	return nil
}
