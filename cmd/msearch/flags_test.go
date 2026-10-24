package main

import (
	"strings"
	"testing"

	"github.com/pigzhu/magnet-search/internal/app"
)

// TestParseArgsPropagatesQuery 校验位置参数会被传进检索流水线的查询词。
// 这条断言是回归网：曾经只把关键词存进 cliOptions.query，忘记同步到
// app.Options.Query，导致命令行按空查询搜索、结果全错。
func TestParseArgsPropagatesQuery(t *testing.T) {
	opts, err := parseArgs([]string{"--engine", "apibay", "--limit", "5", "ubuntu", "24.04"})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if opts.app.Query != "ubuntu 24.04" {
		t.Errorf("流水线的查询词 = %q，期望 %q", opts.app.Query, "ubuntu 24.04")
	}
	if opts.query != "ubuntu 24.04" {
		t.Errorf("命令行的查询词 = %q，期望 %q", opts.query, "ubuntu 24.04")
	}
}

// TestParseArgsBooleanFlagsKeepPositional 校验布尔开关不会吞掉后面的位置参数。
func TestParseArgsBooleanFlagsKeepPositional(t *testing.T) {
	opts, err := parseArgs([]string{"--no-probe", "--verbose", "frieren"})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if opts.app.Query != "frieren" {
		t.Errorf("流水线的查询词 = %q，期望 %q", opts.app.Query, "frieren")
	}
	if opts.app.Probe {
		t.Error("--no-probe 之后探测应被关闭")
	}
	if !opts.verbose {
		t.Error("--verbose 之后应开启调试输出")
	}
}

// TestParseArgsRejectsUnknownFlag 校验未知选项会报错而不是被静默忽略。
func TestParseArgsRejectsUnknownFlag(t *testing.T) {
	if _, err := parseArgs([]string{"--nope", "x"}); err == nil {
		t.Fatal("未知选项应当报错")
	} else if !strings.Contains(err.Error(), "nope") {
		t.Errorf("错误信息应指明选项名，实际: %v", err)
	}
}

// TestParseArgsAccumulatesRepeatedFlags 校验重复指定的选项会被累加而不是覆盖。
// 这条断言是回归网：曾经 --engine 出现两次时只保留最后一个，导致只有一个引擎参与检索。
func TestParseArgsAccumulatesRepeatedFlags(t *testing.T) {
	opts, err := parseArgs([]string{"--engine", "apibay", "--engine", "bt4g,nyaa", "--proxy", "http://a", "--proxy", "http://b"})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	want := []string{"apibay", "bt4g", "nyaa"}
	if len(opts.app.Engines) != len(want) {
		t.Fatalf("引擎列表 = %v，期望 %v", opts.app.Engines, want)
	}
	for i, w := range want {
		if opts.app.Engines[i] != w {
			t.Errorf("引擎列表[%d] = %q，期望 %q", i, opts.app.Engines[i], w)
		}
	}
	if len(opts.app.Proxy) != 2 {
		t.Errorf("代理列表 = %v，期望两个", opts.app.Proxy)
	}
}

// TestParseArgsSplitQuery 校验拆词检索默认开启，--no-split 可以关闭。
func TestParseArgsSplitQuery(t *testing.T) {
	opts, err := parseArgs([]string{"deepseek", "harness"})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if !opts.app.SplitQuery {
		t.Error("拆词检索默认应开启")
	}
	if opts.app.MaxQueryTerms != app.DefaultMaxQueryTerms {
		t.Errorf("MaxQueryTerms = %d，期望默认值 %d", opts.app.MaxQueryTerms, app.DefaultMaxQueryTerms)
	}

	opts, err = parseArgs([]string{"--no-split", "deepseek", "harness"})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if opts.app.SplitQuery {
		t.Error("--no-split 之后拆词检索应关闭")
	}
	if opts.app.Query != "deepseek harness" {
		t.Errorf("--no-split 不应吞掉位置参数，实际查询 %q", opts.app.Query)
	}
}
