package schemawatcher

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

func TestWatcher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root := &FakeRoot{
		StoredPaths: map[string]string{
			"apis/foo.example.com/v1": "/openapi/v3/apis/foo.example.com/v1?hash=1",
		},
		StoredSchemas: map[string]map[string]*spec.Schema{
			"/openapi/v3/apis/foo.example.com/v1?hash=1": {
				"ref/test": &spec.Schema{
					VendorExtensible: spec.VendorExtensible{Extensions: map[string]interface{}{
						extGVK: []schema.GroupVersionKind{
							{
								Group:   "foo.example.com",
								Version: "v1",
								Kind:    "Test",
							},
						},
					}},
					SchemaProps: spec.SchemaProps{
						Type: []string{"string"},
					},
				},
				"ref/unchanged": &spec.Schema{
					VendorExtensible: spec.VendorExtensible{Extensions: map[string]interface{}{
						extGVK: []schema.GroupVersionKind{
							{
								Group:   "foo.example.com",
								Version: "v1",
								Kind:    "Unchanged",
							},
						},
					}},
					SchemaProps: spec.SchemaProps{
						Type: []string{"string"},
					},
				},
			},
		},
	}
	watcher := NewOpenAPIv3Discovery(root, time.Millisecond)
	var gvk schema.GroupVersionKind
	done := make(chan struct{})
	testGVK := schema.GroupVersionKind{
		Group:   "foo.example.com",
		Version: "v1",
		Kind:    "Test",
	}
	missingGVK := testGVK.GroupVersion().WithKind("Missing")
	watcher.Subscribe(ctx, testGVK, missingGVK)

	// basic smoke tests, fatal if not passing
	if watcher.lastSeenSchemas[testGVK] == nil {
		t.Fatalf("missing schema for %v", testGVK)
	}
	if s, ok := watcher.lastSeenSchemas[missingGVK]; !ok {
		t.Fatalf("missing schema for %v", missingGVK)
	} else if s != nil {
		t.Fatalf("unexpected schema for %v", missingGVK)
	}
	if len(watcher.subscribedGVKs[testGVK.GroupVersion()]) == 0 {
		t.Fatalf("missing subscription for %v", testGVK.GroupVersion())
	}

	// update nothing
	watcher.tick(ctx) // would block if a change is detected
	// expect no change

	// dropping Unchanged
	delete(root.StoredSchemas["/openapi/v3/apis/foo.example.com/v1?hash=1"], "ref/unchanged")
	watcher.tick(ctx) // would block if a change is detected
	// expect no change

	// update the path but not the schema
	root.StoredPaths["apis/foo.example.com/v1"] = "/openapi/v3/apis/foo.example.com/v1?hash=2"
	root.StoredSchemas["/openapi/v3/apis/foo.example.com/v1?hash=2"] = root.StoredSchemas["/openapi/v3/apis/foo.example.com/v1?hash=1"]
	delete(root.StoredSchemas, "/openapi/v3/apis/foo.example.com/v1?hash=1")
	watcher.tick(ctx) // would block if a change is detected
	// expect no change

	// update the path AND the schema
	gvk = schema.GroupVersionKind{}
	root.StoredPaths["apis/foo.example.com/v1"] = "/openapi/v3/apis/foo.example.com/v1?hash=3"
	delete(root.StoredSchemas, "/openapi/v3/apis/foo.example.com/v1?hash=2")
	root.StoredSchemas["/openapi/v3/apis/foo.example.com/v1?hash=3"] = map[string]*spec.Schema{
		"ref/test": {
			VendorExtensible: spec.VendorExtensible{Extensions: map[string]interface{}{
				extGVK: []schema.GroupVersionKind{
					{
						Group:   "foo.example.com",
						Version: "v1",
						Kind:    "Test",
					},
				},
			}},
			SchemaProps: spec.SchemaProps{
				Type: []string{"integer"},
			},
		},
	}
	done = make(chan struct{})
	go func() {
		gvk = <-watcher.ChangedGVKsChan
		close(done)
	}()
	watcher.tick(ctx)
	<-done
	if gvk.Kind != "Test" || gvk.Group != "foo.example.com" || gvk.Version != "v1" {
		t.Errorf("wrong gvk returned: %v", gvk)
	}

	// unsubscribe to see if the maps are updated
	watcher.Unsubscribe(ctx, testGVK, missingGVK)
	if _, ok := watcher.lastSeenSchemas[testGVK]; ok {
		t.Fatal("unexpected remaining schema")
	}
	if _, ok := watcher.subscribedGVKs[testGVK.GroupVersion()]; ok {
		t.Fatal("unexpected remaining GV record")
	}
	if _, ok := watcher.lastSeenURLs[root.PathOf(testGVK.GroupVersion())]; ok {
		t.Fatal("unexpected remaining URL record")
	}
}
