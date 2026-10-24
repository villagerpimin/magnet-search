package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/pigzhu/magnet-search/internal/model"
)

// 测试用的合法 info hash。
const (
	variantHashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	variantHashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	variantHashC = "cccccccccccccccccccccccccccccccccccccccc"
	variantHashD = "dddddddddddddddddddddddddddddddddddddddd"
)

// fakeVariantEngine 是可控的假引擎，按查询词返回预设结果。
type fakeVariantEngine struct {
	// name 引擎名
	name string
	// search 按查询参数返回结果或错误
	search func(opts Options) ([]model.Result, error)
	// mu 保护 calls
	mu sync.Mutex
	// calls 已执行的查询次数
	calls int
}

// Name 返回引擎标识。
func (f *fakeVariantEngine) Name() string { return f.name }

// Description 返回引擎描述。
func (f *fakeVariantEngine) Description() string { return "测试引擎" }

// Search 执行一次查询并记录调用次数。
func (f *fakeVariantEngine) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.search(opts)
}

// TestSearchVariantsDedupeKeepsBestRank 校验同一 info hash 只保留引擎位置最好的一条。
func TestSearchVariantsDedupeKeepsBestRank(t *testing.T) {
	e := &fakeVariantEngine{
		name: "fake",
		search: func(opts Options) ([]model.Result, error) {
			switch opts.Query {
			case "deepseek":
				return []model.Result{
					{InfoHash: variantHashA, Rank: 5},
					{InfoHash: variantHashB, Rank: 7},
				}, nil
			case "harness":
				return []model.Result{{InfoHash: variantHashA, Rank: 2}}, nil
			default:
				return nil, fmt.Errorf("意外查询 %q", opts.Query)
			}
		},
	}

	var callbacks int
	var callbackResults []model.Result
	res := SearchVariants(context.Background(), []Engine{e}, Options{Query: "deepseek harness"}, []string{"deepseek", "harness"}, func(name string, results []model.Result, err error) {
		callbacks++
		callbackResults = results
		if err != nil {
			t.Errorf("回调不应有错误: %v", err)
		}
	})

	if callbacks != 1 {
		t.Errorf("回调次数 = %d，期望每个引擎一次", callbacks)
	}
	if len(res.Results) != 2 {
		t.Fatalf("去重后应有 2 条结果，实际 %d", len(res.Results))
	}
	if res.Results[0].InfoHash != variantHashA || res.Results[0].Rank != 2 {
		t.Errorf("应保留 Rank 最小的一条，实际 %+v", res.Results[0])
	}
	if len(callbackResults) != 2 {
		t.Errorf("回调结果条数 = %d，期望 2", len(callbackResults))
	}
	if res.PerEngine["fake"] != 2 {
		t.Errorf("PerEngine = %v，期望 fake 为 2", res.PerEngine)
	}
}

// TestSearchVariantsCallbackOncePerEngine 校验每个引擎只回调一次。
func TestSearchVariantsCallbackOncePerEngine(t *testing.T) {
	search := func(opts Options) ([]model.Result, error) {
		return []model.Result{{InfoHash: variantHashA, Rank: 0}}, nil
	}
	engines := []Engine{
		&fakeVariantEngine{name: "one", search: search},
		&fakeVariantEngine{name: "two", search: search},
	}

	callbacks := map[string]int{}
	SearchVariants(context.Background(), engines, Options{Query: "alpha beta"}, []string{"alpha", "beta"}, func(name string, results []model.Result, err error) {
		callbacks[name]++
	})

	for _, name := range []string{"one", "two"} {
		if callbacks[name] != 1 {
			t.Errorf("引擎 %s 回调次数 = %d，期望 1", name, callbacks[name])
		}
	}
}

// TestSearchVariantsCapsLimit 校验合并结果按 opts.Limit 截断，避免单引擎结果量膨胀。
func TestSearchVariantsCapsLimit(t *testing.T) {
	e := &fakeVariantEngine{
		name: "fake",
		search: func(opts Options) ([]model.Result, error) {
			switch opts.Query {
			case "alpha":
				return []model.Result{
					{InfoHash: variantHashA, Rank: 0},
					{InfoHash: variantHashB, Rank: 1},
				}, nil
			case "beta":
				return []model.Result{
					{InfoHash: variantHashC, Rank: 2},
					{InfoHash: variantHashD, Rank: 3},
				}, nil
			default:
				return nil, fmt.Errorf("意外查询 %q", opts.Query)
			}
		},
	}

	res := SearchVariants(context.Background(), []Engine{e}, Options{Query: "alpha beta", Limit: 3}, []string{"alpha", "beta"}, nil)
	if len(res.Results) != 3 {
		t.Fatalf("结果应截断为 3 条，实际 %d", len(res.Results))
	}
	for i, r := range res.Results {
		if r.Rank != i {
			t.Errorf("第 %d 条 Rank = %d，期望按 Rank 升序", i, r.Rank)
		}
	}
	if res.PerEngine["fake"] != 3 {
		t.Errorf("PerEngine = %v，期望按截断后的条数记录", res.PerEngine)
	}
}

// TestSearchVariantsPartialSuccess 校验只要有一个变体成功，引擎就算成功。
func TestSearchVariantsPartialSuccess(t *testing.T) {
	e := &fakeVariantEngine{
		name: "fake",
		search: func(opts Options) ([]model.Result, error) {
			if opts.Query == "bad" {
				return nil, errors.New("boom")
			}
			return []model.Result{{InfoHash: variantHashA, Rank: 0}}, nil
		},
	}

	var callbackErr error
	res := SearchVariants(context.Background(), []Engine{e}, Options{Query: "good bad"}, []string{"good", "bad"}, func(name string, results []model.Result, err error) {
		callbackErr = err
	})

	if len(res.Failures) != 0 {
		t.Errorf("部分变体失败不应记为引擎失败: %+v", res.Failures)
	}
	if len(res.Results) != 1 {
		t.Errorf("应保留成功变体的结果，实际 %d 条", len(res.Results))
	}
	if callbackErr != nil {
		t.Errorf("回调错误 = %v，期望 nil", callbackErr)
	}
}

// TestSearchVariantsAllFail 校验全部变体失败时只记一次失败并回调错误。
func TestSearchVariantsAllFail(t *testing.T) {
	e := &fakeVariantEngine{
		name: "fake",
		search: func(opts Options) ([]model.Result, error) {
			return nil, fmt.Errorf("查询 %s 失败", opts.Query)
		},
	}

	var callbackErr error
	res := SearchVariants(context.Background(), []Engine{e}, Options{Query: "bad one"}, []string{"bad", "one"}, func(name string, results []model.Result, err error) {
		callbackErr = err
		if results != nil {
			t.Errorf("失败时回调结果应为 nil，实际 %v", results)
		}
	})

	if len(res.Failures) != 1 {
		t.Fatalf("应记录 1 条失败，实际 %d: %+v", len(res.Failures), res.Failures)
	}
	if len(res.Results) != 0 {
		t.Errorf("失败时不应有结果，实际 %d 条", len(res.Results))
	}
	if callbackErr == nil {
		t.Error("回调应收到错误")
	}
	if _, ok := res.PerEngine["fake"]; ok {
		t.Errorf("失败引擎不应出现在 PerEngine: %v", res.PerEngine)
	}
}

// TestSearchVariantsNormalizesAndFallsBack 校验变体去重与空列表回退。
func TestSearchVariantsNormalizesAndFallsBack(t *testing.T) {
	var queries []string
	e := &fakeVariantEngine{
		name: "fake",
		search: func(opts Options) ([]model.Result, error) {
			queries = append(queries, opts.Query)
			return nil, nil
		},
	}

	SearchVariants(context.Background(), []Engine{e}, Options{Query: "fallback"}, []string{"  deepseek  ", "DEEPSEEK", ""}, nil)
	if len(queries) != 1 || queries[0] != "deepseek" {
		t.Errorf("去重后应只查询一次 deepseek，实际 %v", queries)
	}

	queries = nil
	SearchVariants(context.Background(), []Engine{e}, Options{Query: "fallback"}, nil, nil)
	if len(queries) != 1 || queries[0] != "fallback" {
		t.Errorf("空变体列表应回退到 opts.Query，实际 %v", queries)
	}
}
