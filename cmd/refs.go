package cmd

import (
	corev1 "k8s.io/api/core/v1"
)

// podRef represents a ConfigMap or Secret referenced by a pod spec.
type podRef struct {
	Kind     string // "ConfigMap" or "Secret"
	Name     string
	Optional bool
}

// extractPodRefs scans a pod spec for all ConfigMap and Secret references
// across containers, initContainers, ephemeralContainers, and volumes.
// Returns a deduplicated slice.
func extractPodRefs(pod *corev1.Pod) []podRef {
	seen := map[string]bool{}
	var refs []podRef

	add := func(kind, name string, optional bool) {
		if name == "" {
			return
		}
		key := kind + "/" + name
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, podRef{Kind: kind, Name: name, Optional: optional})
	}

	allContainers := make([]corev1.Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	allContainers = append(allContainers, pod.Spec.Containers...)
	allContainers = append(allContainers, pod.Spec.InitContainers...)

	for _, c := range allContainers {
		for _, env := range c.Env {
			if env.ValueFrom == nil {
				continue
			}
			if ref := env.ValueFrom.ConfigMapKeyRef; ref != nil {
				add("ConfigMap", ref.Name, ref.Optional != nil && *ref.Optional)
			}
			if ref := env.ValueFrom.SecretKeyRef; ref != nil {
				add("Secret", ref.Name, ref.Optional != nil && *ref.Optional)
			}
		}
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				opt := ef.ConfigMapRef.Optional != nil && *ef.ConfigMapRef.Optional
				add("ConfigMap", ef.ConfigMapRef.Name, opt)
			}
			if ef.SecretRef != nil {
				opt := ef.SecretRef.Optional != nil && *ef.SecretRef.Optional
				add("Secret", ef.SecretRef.Name, opt)
			}
		}
	}

	for _, c := range pod.Spec.EphemeralContainers {
		for _, env := range c.Env {
			if env.ValueFrom == nil {
				continue
			}
			if ref := env.ValueFrom.ConfigMapKeyRef; ref != nil {
				add("ConfigMap", ref.Name, ref.Optional != nil && *ref.Optional)
			}
			if ref := env.ValueFrom.SecretKeyRef; ref != nil {
				add("Secret", ref.Name, ref.Optional != nil && *ref.Optional)
			}
		}
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				opt := ef.ConfigMapRef.Optional != nil && *ef.ConfigMapRef.Optional
				add("ConfigMap", ef.ConfigMapRef.Name, opt)
			}
			if ef.SecretRef != nil {
				opt := ef.SecretRef.Optional != nil && *ef.SecretRef.Optional
				add("Secret", ef.SecretRef.Name, opt)
			}
		}
	}

	for _, v := range pod.Spec.Volumes {
		if v.ConfigMap != nil {
			opt := v.ConfigMap.Optional != nil && *v.ConfigMap.Optional
			add("ConfigMap", v.ConfigMap.Name, opt)
		}
		if v.Secret != nil {
			opt := v.Secret.Optional != nil && *v.Secret.Optional
			add("Secret", v.Secret.SecretName, opt)
		}
		if v.Projected != nil {
			for _, src := range v.Projected.Sources {
				if src.ConfigMap != nil {
					opt := src.ConfigMap.Optional != nil && *src.ConfigMap.Optional
					add("ConfigMap", src.ConfigMap.Name, opt)
				}
				if src.Secret != nil {
					opt := src.Secret.Optional != nil && *src.Secret.Optional
					add("Secret", src.Secret.Name, opt)
				}
			}
		}
	}

	return refs
}
