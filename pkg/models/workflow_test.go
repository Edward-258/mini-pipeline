package models

import "testing"

// TestNeedsRunner —— 镜像档位判定的声明表：build 是显式声明位（shell 语法 + 强制
// runner），纯 shell 落 busybox（轻活默认轻宿体），hosts 恒强制（ssh 是 busybox 没有的）。
func TestNeedsRunner(t *testing.T) {
	shell := func() *StepSpec { return &StepSpec{Name: "s", Type: StepShell, Script: "echo hi"} }
	cases := []struct {
		name string
		spec *FreestyleSpec
		want bool
	}{
		{"pure shell stays busybox", &FreestyleSpec{Steps: []*StepSpec{shell()}}, false},
		{"build declares toolchain", &FreestyleSpec{Steps: []*StepSpec{{Name: "s", Type: StepBuild, Script: "go build ./..."}}}, true},
		{"empty spec no runner", &FreestyleSpec{}, false},
		{"hosts force runner regardless", &FreestyleSpec{Hosts: []string{"node"}, Steps: []*StepSpec{shell()}}, true},
	}
	for _, tc := range cases {
		if got := tc.spec.NeedsRunner(); got != tc.want {
			t.Errorf("%s: NeedsRunner() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
