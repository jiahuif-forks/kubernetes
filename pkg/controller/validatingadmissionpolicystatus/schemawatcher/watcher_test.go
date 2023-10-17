package schemawatcher

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gregjones/httpcache"

	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type loggingTransport struct {
	http.RoundTripper
}

func (l *loggingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	resp, err := l.RoundTripper.RoundTrip(request)
	if err != nil {
		return resp, err
	}
	if _, ok := resp.Header[httpcache.XFromCache]; ok {
		fmt.Printf("cached: %v\n", request.URL)
	}
	return resp, err
}

func TestDiscovery(t *testing.T) {
	restConfig, err := loadClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	cache := httpcache.NewMemoryCache()
	restConfig.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		t := httpcache.NewTransport(cache)
		t.Transport = rt
		return &loggingTransport{t}
	})
	clientSet, err := clientset.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	b, err := clientSet.RESTClient().Get().AbsPath("/openapi/v3").Do(ctx).Raw()
	if err != nil {
		t.Fatal(err)
	}
	b, err = clientSet.RESTClient().Get().AbsPath("/openapi/v3").Do(ctx).Raw()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile("/tmp/openapiv3.json", b, 0644)
}

func loadClientConfig() (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	if config, err := kubeConfig.ClientConfig(); err == nil {
		return config, nil
	}
	return rest.InClusterConfig()
}
