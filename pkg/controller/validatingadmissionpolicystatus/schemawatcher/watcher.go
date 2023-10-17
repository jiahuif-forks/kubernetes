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

package schemawatcher

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// OpenAPI v3 Terminology
// - GroupVersion (GV): a Kubernetes resource group-version, e.g. Group="foo.example.com", Version="v1". Group may be empty
// - GroupVersionKind (GVK): a Kubernetes resource group-version-kind. e.g. Group="foo.example.com", Version="v1",
//     Kind="CronTab"
// - resource path: the relative path to root of the REST API endpoints. e.g. "apis/foo.example.com/v1"
// - resource endpoint URL: the URL to the OpenAPI v3 documentation that describe the resource. This document includes
//     StoredSchemas, which are what the watcher requires. The URL includes a hash to make it immutable. It looks like
//     "/openapi/v3/apis/foo.example.com/v1?hash=477a3d43f692aeaf1c7f40c0c91bffde3e2e638d8e90c668422373ee82a18521"

// OpenAPIv3Discovery is a schema watcher that uses OpenAPI v3 discovery endpoint, but not the standard discovery client
// due to the client's limitation of cache handling.
type OpenAPIv3Discovery struct {
	root               Root
	schemaPollInterval time.Duration

	// mu protects all maps below
	mu sync.RWMutex

	// subscribedGVKs stores subscribed GVKs, as a map of GroupVersion -> set of kinds
	subscribedGVKs map[schema.GroupVersion]sets.Set[string]

	// lastSeenURLs stores the last seen resource endpoint URL for a resource path
	// only subscribed GVs have entries here.
	lastSeenURLs map[string]string

	// lastSeenSchemas stores the last seen schema for a GVK.
	// This storage doubles as a cache.
	// If a GVK does not have a schema present, the schema is set to nil
	lastSeenSchemas map[schema.GroupVersionKind]*spec.Schema

	// ChangedGVKsChan retrieves GVKs that have their StoredSchemas mutated
	ChangedGVKsChan <-chan schema.GroupVersionKind

	// notificationChan is the send-only version of ChangedGVKsChan
	notificationChan chan<- schema.GroupVersionKind
}

func NewOpenAPIv3Discovery(root Root, schemaPollInterval time.Duration) *OpenAPIv3Discovery {
	notificationChan := make(chan schema.GroupVersionKind)
	return &OpenAPIv3Discovery{
		root:               root,
		schemaPollInterval: schemaPollInterval,
		subscribedGVKs:     make(map[schema.GroupVersion]sets.Set[string]),
		lastSeenURLs:       make(map[string]string),
		lastSeenSchemas:    make(map[schema.GroupVersionKind]*spec.Schema),
		ChangedGVKsChan:    notificationChan,
		notificationChan:   notificationChan,
	}
}

func groupGVKs(gvks []schema.GroupVersionKind) map[schema.GroupVersion][]schema.GroupVersionKind {
	gvToGVKs := make(map[schema.GroupVersion][]schema.GroupVersionKind)
	for _, gvk := range gvks {
		gv := gvk.GroupVersion()
		gvToGVKs[gv] = append(gvToGVKs[gv], gvk)
	}
	return gvToGVKs
}

// Subscribe adds the given GVKs to the watch list and attempts to fetch the schema
// for the GVK if not already cached. If the GVK is already watched (and thus the schema
// already cached), this function does nothing.
func (d *OpenAPIv3Discovery) Subscribe(ctx context.Context, gvks ...schema.GroupVersionKind) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Filter out existing GVKs
	newGVKs := sets.New[schema.GroupVersionKind]()
	for _, gvk := range gvks {
		if _, ok := d.lastSeenSchemas[gvk]; !ok {
			newGVKs.Insert(gvk)
			d.lastSeenSchemas[gvk] = nil
		}
	}
	gvToGVKs := groupGVKs(newGVKs.UnsortedList())

	// Subscribe and fetch all newly referred GVs
	for gv := range gvToGVKs {
		set, ok := d.subscribedGVKs[gv]
		if !ok {
			set = sets.New[string]()
			d.subscribedGVKs[gv] = set
			d.lastSeenURLs[d.root.PathOf(gv)] = ""
		}
		gvks := gvToGVKs[gv]
		for _, gvk := range gvks {
			set.Insert(gvk.Kind)
		}
		err := d.fetchGroupVersion(ctx, gv, gvks)
		if err != nil {
			utilruntime.HandleError(err)
		}
	}
}

// Unsubscribe removes the given GVK from the watch list, and remove the cached schema.
// If the GVK is not yet watched, this function does nothing.
func (d *OpenAPIv3Discovery) Unsubscribe(ctx context.Context, gvks ...schema.GroupVersionKind) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, gvk := range gvks {
		gv := gvk.GroupVersion()
		set, ok := d.subscribedGVKs[gv]
		if !ok {
			return
		}
		set.Delete(gvk.Kind)
		delete(d.lastSeenSchemas, gvk)
		if set.Len() == 0 {
			delete(d.subscribedGVKs, gv)
			delete(d.lastSeenURLs, d.root.PathOf(gv))
		}
	}
}

// Run runs the watcher. It ticks by the set interval.
// The watcher stops upon the cancellation of the context.
func (d *OpenAPIv3Discovery) Run(ctx context.Context) {
	t := time.NewTicker(d.schemaPollInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			d.tick(ctx)
		case <-ctx.Done():
			break
		}
	}
}

func (d *OpenAPIv3Discovery) tick(ctx context.Context) {
	changed, err := d.refresh(ctx)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	for _, gvk := range changed {
		d.notificationChan <- gvk
	}
}

// refresh polls endpoints for all tracked GroupVersion, update cached StoredSchemas for each mutated GVKs
// as needed. Takes a slice of ignored GVKs to enable fetching new StoredSchemas.
// It returns all subscribed GVKs that are detected to be changed.
func (d *OpenAPIv3Discovery) refresh(ctx context.Context) ([]schema.GroupVersionKind, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var allChanged []schema.GroupVersionKind
	pathToURLs, err := d.root.Paths(ctx)
	if err != nil {
		return nil, err
	}

	// keep track of GVs that are no longer present in the root
	disappearedGVs := sets.New[schema.GroupVersion]()

	for path, lastSeen := range d.lastSeenURLs {
		if newURL, ok := pathToURLs[path]; newURL != lastSeen {
			d.lastSeenURLs[path] = newURL
			gv := d.root.GroupVersionOf(path)
			changedGVKs, err := d.refreshFromURL(ctx, gv, newURL)
			if err != nil {
				utilruntime.HandleError(err)
				continue
			}
			allChanged = append(allChanged, changedGVKs...)
		} else if !ok {
			disappearedGVs.Insert(d.root.GroupVersionOf(path))
		}
	}

	// remove records for disappeared GVs
	for gv := range disappearedGVs {
		path := d.root.PathOf(gv)
		d.lastSeenURLs[path] = ""
		for kind := range d.subscribedGVKs[gv] {
			if gvk := gv.WithKind(kind); d.lastSeenSchemas[gvk] != nil {
				allChanged = append(allChanged, gvk)
				d.lastSeenSchemas[gvk] = nil
			}
		}
	}

	return allChanged, nil

}

// fetchGroupVersion fetches StoredSchemas of given GVKs for the given GroupVersion, and update cached StoredSchemas if needed.
// This function does not update path-to-url mapping.
// This function does not cause notifications of changes to be sent.
func (d *OpenAPIv3Discovery) fetchGroupVersion(ctx context.Context, gv schema.GroupVersion, gvks []schema.GroupVersionKind) error {
	pathToURLs, err := d.root.Paths(ctx)
	if err != nil {
		return nil
	}
	url := pathToURLs[d.root.PathOf(gv)]
	schemas, err := d.root.Schemas(ctx, url)
	if err != nil {
		return err
	}
	gvksSet := sets.New[schema.GroupVersionKind](gvks...)
	for _, s := range schemas {
		var gvks []schema.GroupVersionKind
		err := s.Extensions.GetObject(extGVK, &gvks)
		if err != nil {
			return err
		}
		for _, g := range gvks {
			if gvksSet.Has(g) {
				d.compareAndStoreSchema(schemas, g, s)
			}
		}
	}
	return nil
}

// refreshFromURL polls the resource endpoint for the given GroupVersion from the give URL, and update cached
// StoredSchemas as needed.
// It returns all subscribed GVKs that are changed, under the give GroupVersion.
func (d *OpenAPIv3Discovery) refreshFromURL(ctx context.Context, groupVersion schema.GroupVersion, url string) ([]schema.GroupVersionKind, error) {
	var changed []schema.GroupVersionKind
	schemas, err := d.root.Schemas(ctx, url)
	if err != nil {
		return nil, err
	}
	subscribedKinds := d.subscribedGVKs[groupVersion]
	disappearedKinds := subscribedKinds.Clone() // keep record of kinds that are not present in the returned schema.
	for _, s := range schemas {
		var gvks []schema.GroupVersionKind
		err := s.Extensions.GetObject(extGVK, &gvks)
		if err != nil {
			return nil, err
		}
		for _, g := range gvks {
			if subscribedKinds.Has(g.Kind) {
				disappearedKinds.Delete(g.Kind) // the GVK is still in the StoredSchemas, and thus not removed.
				if d.compareAndStoreSchema(schemas, g, s) {
					changed = append(changed, g)
				}
			}
		}
	}
	// set all tracked GVKs that are no longer present to nil
	for k := range disappearedKinds {
		d.lastSeenSchemas[groupVersion.WithKind(k)] = nil
	}
	return changed, nil
}

// compareAndStoreSchema populates the schema for the given GVK, and compare it with the stored version.
// This function returns true and update the cached schema if it detects a change; otherwise, it returns false.
func (d *OpenAPIv3Discovery) compareAndStoreSchema(schemas map[string]*spec.Schema, gvk schema.GroupVersionKind, newSchema *spec.Schema) bool {
	// populating schema is not an expensive operation due to limitations put on CRD and aggregation during their validations
	s, err := resolver.PopulateRefs(func(ref string) (*spec.Schema, bool) {
		s, ok := schemas[strings.TrimPrefix(ref, refPrefix)]
		return s, ok
	}, newSchema)
	if err != nil {
		utilruntime.HandleError(err)
		return false
	}

	cached := d.lastSeenSchemas[gvk]
	if !reflect.DeepEqual(cached, s) {
		d.lastSeenSchemas[gvk] = s
		return true
	}
	return false
}

// ResolveSchema returns the cached schema for the given GVK,
// or ErrSchemaNotFound if the schema of the GVK is not yet known.
// This function does not attempt to refresh the schema.
func (d *OpenAPIv3Discovery) ResolveSchema(gvk schema.GroupVersionKind) (*spec.Schema, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	s := d.lastSeenSchemas[gvk]
	if s == nil {
		return nil, resolver.ErrSchemaNotFound
	}
	return s, nil
}
