package server

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/pigzhu/magnet-search/internal/engine"
	"github.com/pigzhu/magnet-search/internal/model"
)

// probeStubEngine 是自检测试用的假引擎：记录收到的查询词，返回预设结果。
type probeStubEngine struct {
	name    string
	count   int
	err     error
	mu      sync.Mutex
	queries []string
}

// Name 返回引擎名。
func (e *probeStubEngine) Name() string { return e.name }

// Description 返回引擎描述。
func (e *probeStubEngine) Description() string { return "自检假引擎" }

// Search 记录查询词并返回预设结果。
func (e *probeStubEngine) Search(_ context.Context, opts engine.Options) ([]model.Result, error) {
	e.mu.Lock()
	e.queries = append(e.queries, opts.Query)
	e.mu.Unlock()
	if e.err != nil {
		return nil, e.err
	}
	out := make([]model.Result, e.count)
	for i := range out {
		out[i] = model.Result{Title: e.name}
	}
	return out, nil
}

// TestProbeEnginesUsesPerEngineQuery 校验自检按引擎各自的探针词发查询，并如实回报状态。
func TestProbeEnginesUsesPerEngineQuery(t *testing.T) {
	plain := &probeStubEngine{name: "plain", count: 2}
	coded := &probeStubEngine{name: "coded", count: 1}
	failing := &probeStubEngine{name: "failing", err: errors.New("请求失败")}
	specs := []engine.Spec{
		{Name: "plain", Region: engine.RegionAny},
		{Name: "coded", Region: engine.RegionOverseas, ProbeQuery: "SSIS-001"},
		{Name: "failing", Region: engine.RegionOverseas},
		{Name: "missing"}, // 构造失败（没进 built），不该产生状态
	}
	built := map[string]engine.Engine{"plain": plain, "coded": coded, "failing": failing}

	var mu sync.Mutex
	statuses := make(map[string]engineStatus, len(specs))
	probeEngines(context.Background(), specs, built, func(status engineStatus) {
		mu.Lock()
		statuses[status.Name] = status
		mu.Unlock()
	})

	if len(statuses) != 3 {
		t.Fatalf("状态数 = %d，期望 3 个", len(statuses))
	}
	if got := plain.queries; len(got) != 1 || got[0] != engine.DefaultProbeQuery {
		t.Errorf("默认引擎查询词 = %v，期望 [%s]", got, engine.DefaultProbeQuery)
	}
	if got := coded.queries; len(got) != 1 || got[0] != "SSIS-001" {
		t.Errorf("番号引擎查询词 = %v，期望 [SSIS-001]", got)
	}
	if got := statuses["coded"]; !got.OK || got.Count != 1 {
		t.Errorf("coded 状态 = %+v，期望可用且 1 条", got)
	}
	if got := statuses["plain"]; !got.OK || got.Count != 2 {
		t.Errorf("plain 状态 = %+v，期望可用且 2 条", got)
	}
	if got := statuses["failing"]; got.OK || got.Error == "" {
		t.Errorf("failing 状态 = %+v，期望不可用并带原因", got)
	}
}
