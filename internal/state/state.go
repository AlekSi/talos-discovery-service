// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package state implements server state with clusters, affiliates, subscriptions, etc.
package state

import (
	"context"
	"sync"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// State keeps the discovery service state.
type State struct { //nolint:govet
	clusters sync.Map
	logger   *zap.Logger

	mClustersDesc      *prom.Desc
	mAffiliatesDesc    *prom.Desc
	mEndpointsDesc     *prom.Desc
	mSubscriptionsDesc *prom.Desc
	mGCRuns            prom.Counter
	mGCClusters        prom.Counter
	mGCAffiliates      prom.Counter
}

// NewState create new instance of State.
func NewState(logger *zap.Logger) *State {
	return &State{
		logger: logger,
		mClustersDesc: prom.NewDesc(
			"discovery_state_clusters",
			"The current number of clusters in the state.",
			nil, nil,
		),
		mAffiliatesDesc: prom.NewDesc(
			"discovery_state_affiliates",
			"The current number of affiliates in the state.",
			nil, nil,
		),
		mEndpointsDesc: prom.NewDesc(
			"discovery_state_endpoints",
			"The current number of endpoints in the state.",
			nil, nil,
		),
		mSubscriptionsDesc: prom.NewDesc(
			"discovery_state_subscriptions",
			"The current number of subscriptions in the state.",
			nil, nil,
		),
		mGCRuns: prom.NewCounter(prom.CounterOpts{
			Name: "discovery_state_gc_runs_total",
			Help: "The number of GC runs.",
		}),
		mGCClusters: prom.NewCounter(prom.CounterOpts{
			Name: "discovery_state_gc_clusters_total",
			Help: "The total number of GC'ed clusters.",
		}),
		mGCAffiliates: prom.NewCounter(prom.CounterOpts{
			Name: "discovery_state_gc_affiliates_total",
			Help: "The total number of GC'ed affiliates.",
		}),
	}
}

// GetCluster returns cluster by ID, creating it if needed.
func (state *State) GetCluster(id string) *Cluster {
	if v, ok := state.clusters.Load(id); ok {
		return v.(*Cluster)
	}

	v, loaded := state.clusters.LoadOrStore(id, NewCluster(id))
	if !loaded {
		state.logger.Debug("cluster created", zap.String("cluster_id", id))
	}

	return v.(*Cluster)
}

// GarbageCollect recursively each cluster, and remove empty clusters.
func (state *State) GarbageCollect(now time.Time) (removedClusters, removedAffiliates int) {
	state.clusters.Range(func(key, value interface{}) bool {
		cluster := value.(*Cluster) //nolint:errcheck,forcetypeassert
		ra, empty := cluster.GarbageCollect(now)
		removedAffiliates += ra
		if empty {
			state.clusters.Delete(key)
			state.logger.Debug("cluster removed", zap.String("cluster_id", key.(string)))
			removedClusters++
		}

		return true
	})

	state.mGCRuns.Inc()
	state.mGCClusters.Add(float64(removedClusters))
	state.mGCAffiliates.Add(float64(removedAffiliates))

	return
}

// RunGC runs the garbage collection on interval.
func (state *State) RunGC(ctx context.Context, logger *zap.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for ctx.Err() == nil {
		removedClusters, removedAffiliates := state.GarbageCollect(time.Now())
		clusters, affiliates, endpoints, subscriptions := state.stats()

		logFunc := logger.Debug
		if removedClusters > 0 || removedAffiliates > 0 {
			logFunc = logger.Info
		}

		logFunc(
			"garbage collection run",
			zap.Int("removed_clusters", removedClusters),
			zap.Int("removed_affiliates", removedAffiliates),
			zap.Int("current_clusters", clusters),
			zap.Int("current_affiliates", affiliates),
			zap.Int("current_endpoints", endpoints),
			zap.Int("current_subscriptions", subscriptions),
		)

		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func (state *State) stats() (clusters, affiliates, endpoints, subscriptions int) {
	state.clusters.Range(func(key, value interface{}) bool {
		clusters++

		cluster := value.(*Cluster) //nolint:errcheck,forcetypeassert

		a, e, s := cluster.stats()
		affiliates += a
		endpoints += e
		subscriptions += s

		return true
	})

	return
}

// Describe implements prom.Collector interface.
func (state *State) Describe(ch chan<- *prom.Desc) {
	prom.DescribeByCollect(state, ch)
}

// Collect implements prom.Collector interface.
func (state *State) Collect(ch chan<- prom.Metric) {
	clusters, affiliates, endpoints, subscriptions := state.stats()

	ch <- prom.MustNewConstMetric(state.mClustersDesc, prom.GaugeValue, float64(clusters))
	ch <- prom.MustNewConstMetric(state.mAffiliatesDesc, prom.GaugeValue, float64(affiliates))
	ch <- prom.MustNewConstMetric(state.mEndpointsDesc, prom.GaugeValue, float64(endpoints))
	ch <- prom.MustNewConstMetric(state.mSubscriptionsDesc, prom.GaugeValue, float64(subscriptions))

	ch <- state.mGCRuns
	ch <- state.mGCClusters
	ch <- state.mGCAffiliates
}

// Check interfaces.
var (
	_ prom.Collector = (*State)(nil)
)
