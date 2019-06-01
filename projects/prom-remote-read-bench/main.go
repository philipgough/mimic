package main

import (
	"fmt"
	"io/ioutil"
	"path"

	"github.com/bwplotka/gocodeit/abstractions/kubernetes/volumes"
	"github.com/go-openapi/swag"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/bwplotka/gocodeit"
	"github.com/bwplotka/gocodeit/encoding"
	"github.com/bwplotka/gocodeit/providers/prometheus"
)

const (
	selectorName = "app"
)

// This is not the best, but the simplest solution for secrets. See: README.md#Important: Guide & best practices
type Secrets struct{}

func main() {
	gci := gocodeit.New()

	// Make sure to generate at the very end.
	defer gci.Generate()

	// Generate resources for remote read tests.

	// Baseline.
	genRRTestPrometheus(gci, "prom-rr-test")
}

func genRRTestPrometheus(gci *gocodeit.Generator, name string) {
	const (
		replicas = 1

		configVolumeName  = "prometheus-config"
		configVolumeMount = "/etc/prometheus"
		sharedDataPath    = "/data-shared"

		namespace = "bartek"

		httpPort          = 80
		containerHTTPPort = 9090
		pushGatewayPort   = 9091
		httpSidecarPort   = 19190
		grpcSidecarPort   = 19090

		blockgenImage = "improbable/blockgen:master-f39ecb9fa4f"
		// Generate million series.
		blockgenInput = `{
  "type": "gauge",
  "jitter": 20,
  "max": 200000000,
  "min": 100000000,
  "result": {multiplier:1000000, resultType":"vector","result":[{"metric":{"__name__":"kube_pod_container_resource_limits_memory_bytes","cluster":"eu1","container":"addon-resizer","instance":"172.17.0.9:8080","job":"kube-state-metrics","namespace":"kube-system","node":"minikube","pod":"kube-state-metrics-68f6cc566c-vp566"}}]}
}`
		promVersion   = "v2.10.0"
		thanosVersion = "v0.5.0-rc.0"
	)
	var (
		promDataPath    = path.Join(sharedDataPath, "prometheus")
		prometheusImage = fmt.Sprintf("quay.io/prometheus:%s", promVersion)
		thanosImage     = fmt.Sprintf("improbable/thanos:%s", thanosVersion)
	)

	// Empty configuration, we don't need any scrape.
	cfgBytes, err := ioutil.ReadAll(encoding.YAML(prometheus.Config{}))
	if err != nil {
		gocodeit.PanicErr(err)
	}
	promConfigAndMount := volumes.ConfigAndMount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configVolumeName,
			Namespace: namespace,
			Labels: map[string]string{
				selectorName: name,
			},
		},
		VolumeMount: corev1.VolumeMount{
			Name:      configVolumeName,
			MountPath: configVolumeMount,
		},
		Data: map[string]string{
			"prometheus.yaml": string(cfgBytes),
		},
	}

	sharedVM := volumes.VolumeAndMount{
		VolumeMount: corev1.VolumeMount{
			Name:      name,
			MountPath: sharedDataPath,
		},
	}

	srv := corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				selectorName: name,
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
			Selector: map[string]string{
				selectorName: name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       containerHTTPPort,
					TargetPort: intstr.FromInt(containerHTTPPort),
				},
				{
					Name:       "grpc-sidecar",
					Port:       grpcSidecarPort,
					TargetPort: intstr.FromInt(grpcSidecarPort),
				},
				{
					Name:       "http-sidecar",
					Port:       httpSidecarPort,
					TargetPort: intstr.FromInt(httpSidecarPort),
				},
			},
		},
	}

	blockgenInitContainer := corev1.Container{
		Name:    "blockgen",
		Image:   blockgenImage,
		Command: []string{"/bin/blockgen"},
		Args: []string{
			fmt.Sprintf("--input=%s", blockgenInput),
			fmt.Sprintf("--output-dir=%s", promDataPath),
			"--retention=2d",
		},
		VolumeMounts: []corev1.VolumeMount{sharedVM.VolumeMount},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	prometheusContainer := corev1.Container{
		Name:  "prometheus",
		Image: prometheusImage,
		Args: []string{
			fmt.Sprintf("--prometheus.file=%v/prometheus.yaml", sharedDataPath),
			"--log.level=info",
			// Unlimited RR.
			"--storage.remote.read-concurrent-limit=0",
			"--storage.remote.read-sample-limit=0",
			fmt.Sprintf("--storage.tsdb.path=%s", promDataPath),
			"--storage.tsdb.min-block-duration=2h",
			// Avoid compaction for less moving parts in results.
			"--storage.tsdb.max-block-duration=2h",
			"--storage.tsdb.retention=2d",
			"--web.enable-lifecycle=true",
			"--web.enable-admin-api=true",
		},
		Env: []corev1.EnvVar{
			{Name: "HOSTNAME", ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			}},
		},
		ImagePullPolicy: corev1.PullAlways,
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Port: intstr.FromInt(int(containerHTTPPort)),
					Path: "-/ready",
				},
			},
			SuccessThreshold: 3,
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: containerHTTPPort,
			},
		},
		VolumeMounts: volumes.VolumesAndMounts{promConfigAndMount.VolumeAndMount(), sharedVM}.VolumeMounts(),
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: swag.Bool(false),
			RunAsUser:    swag.Int64(1000),
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	thanosSidecarContainer := corev1.Container{
		Name:            "thanos",
		Image:           thanosImage,
		Command:         []string{"thanos"},
		ImagePullPolicy: corev1.PullAlways,
		Args: []string{
			"sidecar",
			"--log.level=debug",
			"--debug.name=$(POD_NAME)",
			fmt.Sprintf("--http-address=0.0.0.0:%d", httpSidecarPort),
			fmt.Sprintf("--grpc-address=0.0.0.0:%d", grpcSidecarPort),
			fmt.Sprintf("--prometheus.url=http://localhost:%d", containerHTTPPort),
			fmt.Sprintf("--tsdb.path=%s", promDataPath),
		},
		Env: []corev1.EnvVar{
			{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			}},
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "m-sidecar",
				ContainerPort: httpSidecarPort,
			},
			{
				Name:          "grpc-sidecar",
				ContainerPort: grpcSidecarPort,
			},
		},
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Port: intstr.FromInt(int(httpSidecarPort)),
					Path: "metrics",
				},
			},
		},
		VolumeMounts: volumes.VolumesAndMounts{sharedVM}.VolumeMounts(),
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	set := appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				selectorName: name,
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    swag.Int32(replicas),
			ServiceName: name,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						selectorName: name,
						"version":    fmt.Sprintf("prometheus%s_thanos%s", promVersion, thanosVersion),
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{blockgenInitContainer},
					Containers:     []corev1.Container{prometheusContainer, thanosSidecarContainer},
					Volumes:        volumes.VolumesAndMounts{promConfigAndMount.VolumeAndMount(), sharedVM}.Volumes(),
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					selectorName: name,
				},
			},
		},
	}

	gci.Add(name+".yaml", encoding.YAML(set, srv, promConfigAndMount.ConfigMap()))
}
