// +build linux

// ebpf_exporter - A Prometheus exporter for Linux block IO statistics.
//
// Copyright 2018 Daniel Swarbrick
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"math"
	"strconv"
	"strings"

	"github.com/iovisor/gobpf/bcc"

	"github.com/prometheus/client_golang/prometheus"
)

type bioStats struct {
	readLat  map[float64]uint64
	writeLat map[float64]uint64
}

type exporter struct {
	bpfMod   *bcc.Module
	readLat  *bcc.Table
	writeLat *bcc.Table
	latency  *prometheus.Desc
}

func newExporter(m *bcc.Module) *exporter {
	e := exporter{
		bpfMod:   m,
		readLat:  bcc.NewTable(m.TableId("read_lat"), m),
		writeLat: bcc.NewTable(m.TableId("write_lat"), m),
		latency: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "bio", "req_latency"),
			"A histogram of bio request latencies in microseconds.",
			[]string{"device", "operation"},
			nil,
		),
	}

	return &e
}

func (e *exporter) Collect(ch chan<- prometheus.Metric) {
	devStats := make(map[string]bioStats)

	for entry := range e.readLat.Iter() {
		devName, bucket := parseKey(entry.Key)

		stats, ok := devStats[devName]
		if !ok {
			// First time seeing this device, initialize new latency map
			stats = bioStats{make(map[float64]uint64), make(map[float64]uint64)}
			devStats[devName] = stats
		}

		if value, err := strconv.ParseUint(entry.Value, 0, 64); err == nil {
			if value > 0 {
				stats.readLat[math.Exp2(float64(bucket))] = value
			}
		}
	}

	// FIXME: Eliminate duplicated code
	for entry := range e.writeLat.Iter() {
		devName, bucket := parseKey(entry.Key)

		stats, ok := devStats[devName]
		if !ok {
			// First time seeing this device, initialize new latency map
			stats = bioStats{make(map[float64]uint64), make(map[float64]uint64)}
			devStats[devName] = stats
		}

		if value, err := strconv.ParseUint(entry.Value, 0, 64); err == nil {
			if value > 0 {
				stats.writeLat[math.Exp2(float64(bucket))] = value
			}
		}
	}

	// Walk devStats map and emit metrics to channel
	for devName, stats := range devStats {
		emit := func(buckets map[float64]uint64, reqOp string) {
			var sampleCount uint64
			var sampleSum float64

			for k, v := range buckets {
				sampleSum += float64(k) * float64(v)
				sampleCount += v
			}

			ch <- prometheus.MustNewConstHistogram(e.latency,
				sampleCount,
				sampleSum,
				buckets,
				devName, reqOp,
			)
		}

		emit(stats.readLat, "read")
		emit(stats.writeLat, "write")
	}
}

func (e *exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.latency
}

// parseKey parses a BPF hash key as created by the BPF program
func parseKey(s string) (string, uint64) {
	fields := strings.Fields(strings.Trim(s, "{ }"))
	label := strings.Trim(fields[0], "\"")
	bucket, _ := strconv.ParseUint(fields[1], 0, 64)
	return label, bucket
}
