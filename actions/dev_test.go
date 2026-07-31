package actions

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcwx/rpm/dag"
	"github.com/vcwx/rpm/models"
)

func TestBuildTargetsByBundle(t *testing.T) {
	graph := dag.NewGraph()

	fooSharedBuild := &models.Target{Name: "shared_build", BundleName: "foo", BundlePath: "apps/foo"}
	fooAppBuild := &models.Target{Name: "app_build", BundleName: "foo", BundlePath: "apps/foo", Deps: []string{":shared_build"}}
	fooAssetsBuild := &models.Target{Name: "assets_build", BundleName: "foo", BundlePath: "apps/foo"}
	fooAppDev := &models.Target{Name: "app_dev", BundleName: "foo", BundlePath: "apps/foo", Deps: []string{":app_build"}}
	fooApiDev := &models.Target{Name: "api_dev", BundleName: "foo", BundlePath: "apps/foo", Deps: []string{":app_build"}}
	barLibBuild := &models.Target{Name: "lib_build", BundleName: "bar", BundlePath: "apps/bar"}
	barDev := &models.Target{Name: "service_dev", BundleName: "bar", BundlePath: "apps/bar", Deps: []string{":lib_build"}}

	for _, target := range []*models.Target{
		fooSharedBuild,
		fooAppBuild,
		fooAssetsBuild,
		fooAppDev,
		fooApiDev,
		barLibBuild,
		barDev,
	} {
		graph.AddTarget(target)
	}

	err := graph.Resolve(map[string]*models.Bundle{
		"foo": {
			Name:    "foo",
			Path:    "apps/foo",
			Targets: []*models.Target{fooSharedBuild, fooAppBuild, fooAssetsBuild, fooAppDev, fooApiDev},
		},
		"bar": {
			Name:    "bar",
			Path:    "apps/bar",
			Targets: []*models.Target{barLibBuild, barDev},
		},
	})
	require.NoError(t, err)

	result := buildTargetsByBundle(graph, []string{fooAppDev.ID(), fooApiDev.ID()})

	require.Len(t, result, 1)
	assert.Equal(t, []string{"foo:app_build", "foo:assets_build", "foo:shared_build"}, result["foo"])
}

func TestBuildTargetsByBundle_DevTargetWithoutBuildDepsStillIncludesBundleBuildTargets(t *testing.T) {
	graph := dag.NewGraph()

	fooBuild := &models.Target{Name: "app_build", BundleName: "foo", BundlePath: "apps/foo"}
	fooAssetsBuild := &models.Target{Name: "assets_build", BundleName: "foo", BundlePath: "apps/foo"}
	fooDev := &models.Target{Name: "app_dev", BundleName: "foo", BundlePath: "apps/foo"}

	for _, target := range []*models.Target{
		fooBuild,
		fooAssetsBuild,
		fooDev,
	} {
		graph.AddTarget(target)
	}

	err := graph.Resolve(map[string]*models.Bundle{
		"foo": {
			Name:    "foo",
			Path:    "apps/foo",
			Targets: []*models.Target{fooBuild, fooAssetsBuild, fooDev},
		},
	})
	require.NoError(t, err)

	result := buildTargetsByBundle(graph, []string{fooDev.ID()})

	require.Len(t, result, 1)
	assert.Equal(t, []string{"foo:app_build", "foo:assets_build"}, result["foo"])
}

func TestBundleBuildCoordinator_DedupesConcurrentBuilds(t *testing.T) {
	plans := map[string][]string{
		"foo": {"foo:app_build"},
	}

	var buildCalls atomic.Int32
	coordinator := newBundleBuildCoordinator(plans, func(ctx context.Context, bundleName string, targetIDs []string) error {
		buildCalls.Add(1)
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	var wg sync.WaitGroup
	statuses := make(chan buildStatus, 3)
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := coordinator.Build(context.Background(), "foo")
			statuses <- status
			errs <- err
		}()
	}
	wg.Wait()
	close(statuses)
	close(errs)

	var ranCount int
	var sharedCount int
	for status := range statuses {
		if status == buildStatusRan {
			ranCount++
		}
		if status == buildStatusShared {
			sharedCount++
		}
	}

	for err := range errs {
		require.NoError(t, err)
	}

	assert.Equal(t, 1, ranCount)
	assert.Equal(t, 2, sharedCount)
	assert.Equal(t, int32(1), buildCalls.Load())
}

func TestBundleBuildCoordinator_PropagatesSharedError(t *testing.T) {
	plans := map[string][]string{
		"foo": {"foo:app_build"},
	}
	expectedErr := errors.New("build failed")

	var buildCalls atomic.Int32
	coordinator := newBundleBuildCoordinator(plans, func(ctx context.Context, bundleName string, targetIDs []string) error {
		buildCalls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return expectedErr
	})

	var wg sync.WaitGroup
	statuses := make(chan buildStatus, 3)
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := coordinator.Build(context.Background(), "foo")
			statuses <- status
			errs <- err
		}()
	}
	wg.Wait()
	close(statuses)
	close(errs)

	var ranCount int
	var sharedCount int
	for status := range statuses {
		if status == buildStatusRan {
			ranCount++
		}
		if status == buildStatusShared {
			sharedCount++
		}
	}

	for err := range errs {
		require.ErrorIs(t, err, expectedErr)
	}

	assert.Equal(t, 1, ranCount)
	assert.Equal(t, 2, sharedCount)
	assert.Equal(t, int32(1), buildCalls.Load())
}

func TestBundleBuildCoordinator_DedupesSequentialCallsWithinWindow(t *testing.T) {
	plans := map[string][]string{
		"foo": {"foo:app_build"},
	}

	var buildCalls atomic.Int32
	coordinator := newBundleBuildCoordinator(plans, func(ctx context.Context, bundleName string, targetIDs []string) error {
		buildCalls.Add(1)
		return nil
	})

	status1, err := coordinator.Build(context.Background(), "foo")
	require.NoError(t, err)
	assert.Equal(t, buildStatusRan, status1)

	status2, err := coordinator.Build(context.Background(), "foo")
	require.NoError(t, err)
	assert.Equal(t, buildStatusShared, status2)

	assert.Equal(t, int32(1), buildCalls.Load())
}

func TestBundleBuildCoordinator_NoBuildTargetsStillCoalesces(t *testing.T) {
	coordinator := newBundleBuildCoordinator(map[string][]string{}, func(ctx context.Context, bundleName string, targetIDs []string) error {
		return nil
	})

	status1, err := coordinator.Build(context.Background(), "foo")
	require.NoError(t, err)
	assert.Equal(t, buildStatusNoop, status1)

	status2, err := coordinator.Build(context.Background(), "foo")
	require.NoError(t, err)
	assert.Equal(t, buildStatusShared, status2)
}
