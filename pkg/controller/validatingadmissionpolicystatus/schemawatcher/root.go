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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/rest"
	"k8s.io/kube-openapi/pkg/handler3"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

const openAPIv3RootEndpoint = "/openapi/v3"

type Root interface {
	// PathOf returns the API path the schema for the given groupVersion
	PathOf(groupVersion schema.GroupVersion) string

	// GroupVersionOf returns the GroupVersion from a path
	GroupVersionOf(path string) schema.GroupVersion

	// Paths returns a map of all API StoredPaths to the corresponding URL
	Paths(ctx context.Context) (map[string]string, error)

	// Schemas retrieves all StoredSchemas from a given url, in the form of ref -> Schema
	Schemas(ctx context.Context, url string) (map[string]*spec.Schema, error)
}

var _ Root = (*OpenAPIv3Root)(nil)

type OpenAPIv3Root struct {
	RESTClient rest.Interface
}

func (r *OpenAPIv3Root) GroupVersionOf(path string) schema.GroupVersion {
	gv, err := pathToGroupVersion(path)
	if err != nil { // should never happen
		return schema.GroupVersion{}
	}
	return gv
}

func (r *OpenAPIv3Root) PathOf(groupVersion schema.GroupVersion) string {
	return resourcePathFromGV(groupVersion)
}

func (r *OpenAPIv3Root) Paths(ctx context.Context) (map[string]string, error) {
	rawResponse, err := r.RESTClient.Get().
		AbsPath("/openapi/v3").
		Do(ctx).
		Raw()

	if err != nil {
		return nil, err
	}

	discovery := &handler3.OpenAPIV3Discovery{}
	err = json.Unmarshal(rawResponse, discovery)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)

	for k, v := range discovery.Paths {
		result[k] = v.ServerRelativeURL
	}
	return result, nil
}

func (r *OpenAPIv3Root) Schemas(ctx context.Context, url string) (map[string]*spec.Schema, error) {
	// borrowed from staging/src/k8s.io/client-go/openapi/groupversion.go
	request := r.RESTClient.Get().SetHeader("Accept", runtime.ContentTypeJSON)
	if strings.HasPrefix(url, openAPIv3RootEndpoint) {
		request = request.RequestURI(url)
	} else {
		request = request.AbsPath(url)
	}
	raw, err := request.Do(ctx).Raw()
	if err != nil {
		return nil, err
	}
	resp := new(schemaResponse)
	err = json.Unmarshal(raw, resp)
	if err != nil {
		return nil, err
	}
	return resp.Components.Schemas, nil
}

func resourcePathFromGV(gv schema.GroupVersion) string {
	if len(gv.Group) == 0 {
		return fmt.Sprintf("api/%s", gv.Version)
	}
	return fmt.Sprintf("apis/%s/%s", gv.Group, gv.Version)
}

// pathToGroupVersion is a helper function parsing the passed relative
// url into a GroupVersion.
//
//	Example: apis/apps/v1 -> GroupVersion{Group: "apps", Version: "v1"}
//	Example: api/v1 -> GroupVersion{Group: "", Version: "v1"}
//
// copied from private function of staging/src/k8s.io/client-go/openapi3/root.go
func pathToGroupVersion(path string) (schema.GroupVersion, error) {
	var gv schema.GroupVersion
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return gv, fmt.Errorf("unable to parse api relative path: %s", path)
	}
	apiPrefix := parts[0]
	if apiPrefix == "apis" {
		// Example: apis/apps (without version)
		if len(parts) < 3 {
			return gv, fmt.Errorf("group without Version not allowed")
		}
		gv.Group = parts[1]
		gv.Version = parts[2]
	} else if apiPrefix == "api" {
		gv.Version = parts[1]
	} else {
		return gv, fmt.Errorf("unable to parse api relative path: %s", path)
	}
	return gv, nil
}

type schemaResponse struct {
	Components struct {
		Schemas map[string]*spec.Schema `json:"StoredSchemas"`
	} `json:"components"`
}

type FakeRoot struct {
	StoredPaths   map[string]string
	StoredSchemas map[string]map[string]*spec.Schema
}

func (f *FakeRoot) PathOf(groupVersion schema.GroupVersion) string {
	return resourcePathFromGV(groupVersion)
}

func (f *FakeRoot) GroupVersionOf(path string) schema.GroupVersion {
	gv, _ := pathToGroupVersion(path)
	return gv
}

func (f *FakeRoot) Paths(ctx context.Context) (map[string]string, error) {
	return f.StoredPaths, nil
}

func (f *FakeRoot) Schemas(ctx context.Context, url string) (map[string]*spec.Schema, error) {
	return f.StoredSchemas[url], nil
}

var _ Root = (*FakeRoot)(nil)
