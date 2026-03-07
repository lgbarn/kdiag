package cmd

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestExtractPodRefs_EnvValueFrom(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{
							Name: "DB_HOST",
							ValueFrom: &corev1.EnvVarSource{
								ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "db-config"},
								},
							},
						},
						{
							Name: "DB_PASS",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "db-secret"},
								},
							},
						},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	cmFound := false
	secFound := false
	for _, r := range refs {
		if r.Kind == "ConfigMap" && r.Name == "db-config" && !r.Optional {
			cmFound = true
		}
		if r.Kind == "Secret" && r.Name == "db-secret" && !r.Optional {
			secFound = true
		}
	}
	if !cmFound {
		t.Error("expected ConfigMap ref 'db-config' not found")
	}
	if !secFound {
		t.Error("expected Secret ref 'db-secret' not found")
	}
}

func TestExtractPodRefs_EnvFrom(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					EnvFrom: []corev1.EnvFromSource{
						{ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
						}},
						{SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "app-secret"},
						}},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
}

func TestExtractPodRefs_Volumes(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "config-vol",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "vol-cm"},
						},
					},
				},
				{
					Name: "secret-vol",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "vol-secret",
						},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
}

func TestExtractPodRefs_Optional(t *testing.T) {
	optional := true
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{
							Name: "OPT",
							ValueFrom: &corev1.EnvVarSource{
								ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "opt-cm"},
									Optional:             &optional,
								},
							},
						},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !refs[0].Optional {
		t.Error("expected ref to be optional")
	}
}

func TestExtractPodRefs_Projected(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "projected-vol",
					VolumeSource: corev1.VolumeSource{
						Projected: &corev1.ProjectedVolumeSource{
							Sources: []corev1.VolumeProjection{
								{ConfigMap: &corev1.ConfigMapProjection{
									LocalObjectReference: corev1.LocalObjectReference{Name: "proj-cm"},
								}},
								{Secret: &corev1.SecretProjection{
									LocalObjectReference: corev1.LocalObjectReference{Name: "proj-secret"},
								}},
							},
						},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
}

func TestExtractPodRefs_InitContainers(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{
					EnvFrom: []corev1.EnvFromSource{
						{ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "init-cm"},
						}},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Name != "init-cm" {
		t.Errorf("expected 'init-cm', got %q", refs[0].Name)
	}
}

func TestExtractPodRefs_Dedup(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{Name: "A", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "same-cm"},
							},
						}},
						{Name: "B", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "same-cm"},
							},
						}},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 1 {
		t.Fatalf("expected 1 deduped ref, got %d", len(refs))
	}
}
