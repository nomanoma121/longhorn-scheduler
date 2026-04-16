// longhorn-scheduler
// Copyright (c) 2026-present NAVER Corp.
// Apache-2.0

package main

import (
	"os"

	"k8s.io/component-base/cli"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"

	"github.com/naver/longhorn-scheduler/pkg"
)

func main() {
	cmd := app.NewSchedulerCommand(
		app.WithPlugin(pkg.Name, pkg.New),
	)
	os.Exit(cli.Run(cmd))
}
