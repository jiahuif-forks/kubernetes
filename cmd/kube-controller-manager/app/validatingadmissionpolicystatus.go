/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package app

import (
	"context"

	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/controller-manager/controller"
	"k8s.io/kubernetes/cmd/kube-controller-manager/names"
	"k8s.io/kubernetes/pkg/controller/validatingadmissionpolicystatus"
	"k8s.io/kubernetes/pkg/controller/validatingadmissionpolicystatus/schemawatcher"
)

func startValidatingAdmissionPolicyStatusController(ctx context.Context, controllerContext ControllerContext) (controller.Interface, bool, error) {
	// KCM won't start the controller without the feature gate set.

	client, err := clientset.NewForConfig(controllerContext.ClientBuilder.ConfigOrDie(names.ValidatingAdmissionPolicyStatusController))
	if err != nil {
		return nil, false, err
	}
	schemaWatcher := schemawatcher.NewOpenAPIv3Discovery(&schemawatcher.OpenAPIv3Root{
		RESTClient: client.RESTClient(),
	}, controllerContext.ComponentConfig.ValidatingAdmissionPolicyStatusController.SchemaPollInterval.Duration)

	c, err := validatingadmissionpolicystatus.NewController(
		controllerContext.InformerFactory.Admissionregistration().V1beta1().ValidatingAdmissionPolicies(),
		controllerContext.ClientBuilder.ClientOrDie(names.ValidatingAdmissionPolicyStatusController).AdmissionregistrationV1beta1().ValidatingAdmissionPolicies(),
		controllerContext.RESTMapper,
		schemaWatcher,
	)

	go c.Run(ctx, int(controllerContext.ComponentConfig.ValidatingAdmissionPolicyStatusController.ConcurrentPolicySyncs))
	return nil, true, err
}
