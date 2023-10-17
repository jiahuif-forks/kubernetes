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

	// Paths returns a map of all API paths to the corresponding URL
	Paths(ctx context.Context) (map[string]string, error)

	// Schemas retrieves all schemas from a given url, in the form of ref -> Schema
	Schemas(ctx context.Context, url string) (map[string]*spec.Schema, error)
}

type OpenAPIv3Root struct {
	restClient rest.RESTClient
}

func (r *OpenAPIv3Root) PathOf(groupVersion schema.GroupVersion) string {
	return resourcePathFromGV(groupVersion)
}

func (r *OpenAPIv3Root) Paths(ctx context.Context) (map[string]string, error) {
	rawResponse, err := r.restClient.Get().
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
	request := r.restClient.Get().SetHeader("Accept", runtime.ContentTypeJSON)
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
	var resourcePath string
	if len(gv.Group) == 0 {
		resourcePath = fmt.Sprintf("api/%s", gv.Version)
	} else {
		resourcePath = fmt.Sprintf("apis/%s/%s", gv.Group, gv.Version)
	}
	return resourcePath
}

type schemaResponse struct {
	Components struct {
		Schemas map[string]*spec.Schema `json:"schemas"`
	} `json:"components"`
}
