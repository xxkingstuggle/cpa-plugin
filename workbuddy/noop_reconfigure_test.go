package main

import (
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// storeReadySnapshot installs a ready snapshot for authID at the runtime's
// current configGeneration, mirroring what ensureForAuth commits after a
// successful catalog load.
func storeReadySnapshot(t *testing.T, r *modelRuntime, authID string) modelReadinessSnapshot {
	t.Helper()
	slot := r.authSlot(authID)
	gen := r.configGeneration.Load()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	snap := modelReadinessSnapshot{
		State:            modelReady,
		ModelSource:      modelSourceFresh,
		MetadataSource:   modelSourceFresh,
		Models:           []pluginapi.ModelInfo{},
		configGeneration: gen,
	}
	return storeModelReadinessSnapshot(slot, snap)
}

// Regression for the 2026-09-13 20:54:49 burst: the host re-applies the
// same workbuddy config after unrelated auth-file churn (other plugins
// refreshing tokens). That reconfigure must NOT bump configGeneration,
// because bumping invalidates every ready snapshot and executors 503 with
// "WorkBuddy model catalog is not ready" until a full catalog reload lands.
func TestCommitFeatureRuntimeNoopKeepsGenerationAndSnapshots(t *testing.T) {
	r := &modelRuntime{}
	r.commitFeatureRuntime(testFeatureConfig(t))
	storeReadySnapshot(t, r, "auth-a")

	before := r.configGeneration.Load()
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.commitFeatureRuntime(testFeatureConfig(t))
		}()
	}
	wg.Wait()

	if after := r.configGeneration.Load(); after != before {
		t.Fatalf("identical reconfigure bumped generation: %d -> %d", before, after)
	}
	if snap := r.snapshotForAuthID("auth-a"); snap.State != modelReady {
		t.Fatalf("ready snapshot lost after no-op reconfigure: state=%v", snap.State)
	}
}

func TestCommitFeatureRuntimeChangeBumpsGeneration(t *testing.T) {
	r := &modelRuntime{}
	first := r.commitFeatureRuntime(testFeatureConfig(t))
	next := testFeatureConfig(t)
	next.enterpriseCredits = !next.enterpriseCredits
	if got := r.commitFeatureRuntime(next); got != first+1 {
		t.Fatalf("generation must bump on real config change: %d -> %d", first, got)
	}
}

func TestCommitFeatureRuntimeModelListChangeBumpsGeneration(t *testing.T) {
	r := &modelRuntime{}
	first := r.commitFeatureRuntime(testFeatureConfig(t))
	next := testFeatureConfig(t)
	next.configuredModels = []string{"glm-5.3-flash"}
	if got := r.commitFeatureRuntime(next); got != first+1 {
		t.Fatalf("generation must bump when model list changes: %d -> %d", first, got)
	}
}

func testFeatureConfig(t *testing.T) *featureRuntimeConfig {
	t.Helper()
	cfg, err := parseFeatureRuntime(nil)
	if err != nil {
		t.Fatalf("parseFeatureRuntime: %v", err)
	}
	return cfg
}
