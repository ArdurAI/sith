// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/ArdurAI/sith/internal/connector"
)

func TestLoadWatchBootstrapPaginatesConsistentSnapshot(t *testing.T) {
	t.Parallel()
	const opaqueToken = "opaque+/=token"
	var options []metav1.ListOptions
	lister := watchListFunc(func(_ context.Context, current metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		options = append(options, current)
		switch current.Continue {
		case "":
			return watchList("rv-7", opaqueToken, "alpha", "bravo"), nil
		case opaqueToken:
			return watchList("rv-7", "", "charlie"), nil
		default:
			return nil, fmt.Errorf("unexpected continuation")
		}
	})
	bootstrap, err := loadWatchBootstrap(context.Background(), lister, watchBootstrapLimits{
		pageSize: 2, objectBudget: 4, pageBudget: 3,
	})
	if err != nil {
		t.Fatalf("loadWatchBootstrap() error = %v", err)
	}
	if len(options) != 2 || options[0].Limit != 2 || options[0].Continue != "" ||
		options[1].Limit != 2 || options[1].Continue != opaqueToken {
		t.Fatalf("list options = %#v, want bounded pages with opaque continuation", options)
	}
	if bootstrap.resourceVersion != "rv-7" || !slices.Equal(watchObjectNames(bootstrap.objects), []string{"alpha", "bravo", "charlie"}) {
		t.Fatalf("bootstrap = %#v, want one complete rv-7 snapshot", bootstrap)
	}
}

func TestLoadWatchBootstrapRejectsIncompleteSnapshots(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		limits  watchBootstrapLimits
		lister  watchListFunc
		wantErr string
	}{
		{
			name:   "object budget",
			limits: watchBootstrapLimits{pageSize: 2, objectBudget: 2, pageBudget: 2},
			lister: func(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error) {
				return watchList("rv-1", "more", "alpha", "bravo"), nil
			},
			wantErr: "2-object budget",
		},
		{
			name:   "nil response",
			limits: watchBootstrapLimits{pageSize: 1, objectBudget: 2, pageBudget: 2},
			lister: func(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error) {
				return nil, nil
			},
			wantErr: "empty list response",
		},
		{
			name:   "ignored page limit",
			limits: watchBootstrapLimits{pageSize: 1, objectBudget: 3, pageBudget: 3},
			lister: func(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error) {
				return watchList("rv-1", "", "alpha", "bravo"), nil
			},
			wantErr: "ignored the requested page limit",
		},
		{
			name:   "empty resource version",
			limits: watchBootstrapLimits{pageSize: 1, objectBudget: 2, pageBudget: 2},
			lister: func(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error) {
				return watchList("", "", "alpha"), nil
			},
			wantErr: "empty resourceVersion",
		},
		{
			name:   "changed resource version",
			limits: watchBootstrapLimits{pageSize: 1, objectBudget: 3, pageBudget: 3},
			lister: func(_ context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
				if options.Continue == "" {
					return watchList("rv-1", "next", "alpha"), nil
				}
				return watchList("rv-2", "", "bravo"), nil
			},
			wantErr: "resourceVersion changed",
		},
		{
			name:   "continuation cycle",
			limits: watchBootstrapLimits{pageSize: 1, objectBudget: 3, pageBudget: 3},
			lister: func(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error) {
				return watchList("rv-1", "cycle", "alpha"), nil
			},
			wantErr: "repeated a continuation token",
		},
		{
			name:   "page budget",
			limits: watchBootstrapLimits{pageSize: 1, objectBudget: 3, pageBudget: 1},
			lister: func(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error) {
				return watchList("rv-1", "next", "alpha"), nil
			},
			wantErr: "1-page budget",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bootstrap, err := loadWatchBootstrap(context.Background(), test.lister, test.limits)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("loadWatchBootstrap() = %#v, %v, want %q error", bootstrap, err, test.wantErr)
			}
			if len(bootstrap.objects) != 0 || bootstrap.resourceVersion != "" {
				t.Fatalf("failed bootstrap leaked partial snapshot: %#v", bootstrap)
			}
		})
	}
}

func TestLoadWatchBootstrapCancellationDiscardsPartialPages(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	lister := watchListFunc(func(ctx context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		if options.Continue == "" {
			return watchList("rv-1", "next", "alpha"), nil
		}
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	bootstrap, err := loadWatchBootstrap(ctx, lister, watchBootstrapLimits{
		pageSize: 1, objectBudget: 3, pageBudget: 3,
	})
	if !errors.Is(err, context.Canceled) || len(bootstrap.objects) != 0 {
		t.Fatalf("loadWatchBootstrap() = %#v, %v, want canceled empty result", bootstrap, err)
	}
}

func TestLoadWatchBootstrapSanitizesExpiredContinuation(t *testing.T) {
	t.Parallel()
	const bodyMarker = "response-body-marker"
	lister := watchListFunc(func(_ context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		if options.Continue == "" {
			return watchList("rv-1", "expired-token", "alpha"), nil
		}
		return nil, apierrors.NewResourceExpired(bodyMarker)
	})
	_, err := loadWatchBootstrap(context.Background(), lister, watchBootstrapLimits{
		pageSize: 1, objectBudget: 3, pageBudget: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "continuation expired") ||
		strings.Contains(err.Error(), bodyMarker) || strings.Contains(err.Error(), "expired-token") {
		t.Fatalf("loadWatchBootstrap() error = %v, want sanitized expiration", err)
	}
}

func TestLoadWatchBootstrapSanitizesContinuationFailure(t *testing.T) {
	t.Parallel()
	const (
		bodyMarker  = "generic-response-body-marker"
		tokenMarker = "generic-continuation-token"
	)
	lister := watchListFunc(func(_ context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		if options.Continue == "" {
			return watchList("rv-1", tokenMarker, "alpha"), nil
		}
		return nil, fmt.Errorf("remote failure: %s %s", bodyMarker, tokenMarker)
	})
	_, err := loadWatchBootstrap(context.Background(), lister, watchBootstrapLimits{
		pageSize: 1, objectBudget: 3, pageBudget: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "list page 2 failed") ||
		strings.Contains(err.Error(), bodyMarker) || strings.Contains(err.Error(), tokenMarker) {
		t.Fatalf("loadWatchBootstrap() error = %v, want sanitized continuation failure", err)
	}
}

func TestWatchBootstrapFailureEmitsErrorWithoutOpeningWatch(t *testing.T) {
	t.Parallel()
	client := fakeClient()
	client.PrependReactor("list", "pods", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, watchList("rv-1", "cycle", "alpha"), nil
	})
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha"))),
		withProbe(func(context.Context, *rest.Config) error { return nil }),
		withDynamicFactory(func(*rest.Config) (dynamic.Interface, error) { return client, nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := adapter.Watch(ctx, "Pod")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	event := receiveWatchEvent(ctx, t, events)
	if event.Type != connector.WatchError || event.Err == nil ||
		!strings.Contains(event.Err.Error(), "repeated a continuation token") {
		t.Fatalf("watch event = %#v, want fail-closed bootstrap error", event)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "watch" {
			t.Fatalf("watch opened after failed bootstrap: %#v", action)
		}
	}
}

type watchListFunc func(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error)

func (function watchListFunc) List(
	ctx context.Context,
	options metav1.ListOptions,
) (*unstructured.UnstructuredList, error) {
	return function(ctx, options)
}

func watchList(resourceVersion, continueToken string, names ...string) *unstructured.UnstructuredList {
	list := &unstructured.UnstructuredList{Items: make([]unstructured.Unstructured, 0, len(names))}
	list.SetAPIVersion("v1")
	list.SetKind("PodList")
	list.SetResourceVersion(resourceVersion)
	list.SetContinue(continueToken)
	for _, name := range names {
		list.Items = append(list.Items, *pod(name, "apps", "registry.example/test:v1", nil))
	}
	return list
}

func watchObjectNames(objects []unstructured.Unstructured) []string {
	names := make([]string, 0, len(objects))
	for _, object := range objects {
		names = append(names, object.GetName())
	}
	return names
}
