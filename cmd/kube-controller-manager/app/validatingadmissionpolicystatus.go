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

	admissionregistrationv1alpha1 "k8s.io/api/admissionregistration/v1alpha1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/controller-manager/controller"
	"k8s.io/kubernetes/pkg/controller/validatingadmissionpolicystatus"
)

var validatingAdmissionPolicyResource = admissionregistrationv1alpha1.SchemeGroupVersion.WithResource("validatingadmissionpolicies")

func startValidatingAdmissionPolicyStatusController(ctx context.Context, controllerContext ControllerContext) (controller.Interface, bool, error) {
	const registeredControllerName = "validatingadmissionpolicy-status-controller"
	// intended check against served resource but not feature gate.
	// KCM won't start the controller without the feature gate set.
	if !controllerContext.AvailableResources[validatingAdmissionPolicyResource] {
		return nil, false, nil
	}
	config := controllerContext.ClientBuilder.ConfigOrDie(registeredControllerName)
	extensionsClient, err := apiextensions.NewForConfig(config)
	if err != nil {
		return nil, false, err
	}
	c, err := validatingadmissionpolicystatus.NewController(
		controllerContext.InformerFactory.Admissionregistration().V1alpha1().ValidatingAdmissionPolicies(),
		controllerContext.ClientBuilder.ClientOrDie(registeredControllerName).AdmissionregistrationV1alpha1().ValidatingAdmissionPolicies(),
		controllerContext.APIExtensionsInformerFactory.Apiextensions().V1().CustomResourceDefinitions(),
		extensionsClient.ApiextensionsV1().CustomResourceDefinitions(),
		controllerContext.RESTMapper,
	)
	if err != nil {
		return nil, false, err
	}
	go c.Run(ctx, int(controllerContext.ComponentConfig.ValidatingAdmissionPolicyStatusController.ConcurrentPolicySyncs))
	return nil, true, err
}
