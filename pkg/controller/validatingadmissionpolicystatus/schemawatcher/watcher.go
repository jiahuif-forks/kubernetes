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
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

// OpenAPIv3Discovery is a schema watcher that uses OpenAPI v3 discovery endpoint, but not the standard discovery client
// due to the client's limitation of cache handling.
type OpenAPIv3Discovery struct {
	root Root

	// lastSeenURLs stores the last seen group-version discovery URL for given path
	// only subscribed GVs have entries here.
	lastSeenURLs map[string]string
}

func (d *OpenAPIv3Discovery) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	for {
		select {
		case <-t.C:
			err := d.refresh(ctx)
			if err != nil {
				utilruntime.HandleError(err)
			}
		case <-ctx.Done():
			break
		}
	}
}

func (d *OpenAPIv3Discovery) refresh(ctx context.Context) error {
	pathToURLs, err := d.root.Paths(ctx)
	if err != nil {
		return err
	}
	_ = pathToURLs
	return nil
}
