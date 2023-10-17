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

package validatingadmissionpolicystatus

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
)

// referenceTracker tracks bidirectional extensionTypes <-> policy name references
type referenceTracker struct {
	policyNameToGVKs map[string]sets.Set[schema.GroupVersionKind]
	gvkToPolicyNames map[schema.GroupVersionKind]sets.Set[string]
}

func newReferenceTracker() *referenceTracker {
	return &referenceTracker{
		policyNameToGVKs: map[string]sets.Set[schema.GroupVersionKind]{},
		gvkToPolicyNames: map[schema.GroupVersionKind]sets.Set[string]{},
	}
}

// observePolicyUpdate should be called when a policy is being created, updated, or deleted.
// gvks should be the set of GVKs the policy now refers to. Pass nil gvks for deletion.
// It returns what GVKs to subscribe and unsubscribe.
func (t *referenceTracker) observePolicyUpdate(policyName string, gvks []schema.GroupVersionKind) (toSubscribe, toUnsubscribe []schema.GroupVersionKind) {
	newGVKs := sets.New[schema.GroupVersionKind](gvks...)
	currentGVKs, ok := t.policyNameToGVKs[policyName]
	if !ok {
		currentGVKs = sets.New[schema.GroupVersionKind]()
	}
	added, removed := newGVKs.Difference(currentGVKs), currentGVKs.Difference(newGVKs)
	for gvk := range added {
		s, ok := t.gvkToPolicyNames[gvk]
		if !ok {
			s = sets.New[string]()
			t.gvkToPolicyNames[gvk] = s
			toSubscribe = append(toSubscribe, gvk)
		}
		s.Insert(policyName)
	}
	for gvk := range removed {
		s, ok := t.gvkToPolicyNames[gvk]
		if !ok {
			continue
		}
		s.Delete(policyName)
		if s.Len() == 0 {
			delete(t.gvkToPolicyNames, gvk)
			toUnsubscribe = append(toUnsubscribe, gvk)
		}
	}
	t.policyNameToGVKs[policyName] = sets.New[schema.GroupVersionKind](gvks...)
	return
}

func (t *referenceTracker) affectedPolicies(gvk schema.GroupVersionKind) []string {
	return t.gvkToPolicyNames[gvk].UnsortedList()
}
