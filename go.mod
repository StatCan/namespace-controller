// This is a generated file. Do not edit directly.

module github.com/StatCan/namespace-controller

go 1.13

require (
	github.com/docker/spdystream v0.0.0-20160310174837-449fdfce4d96 // indirect
	github.com/go-openapi/spec v0.19.3 // indirect
	github.com/spf13/cobra v1.1.3
	golang.org/x/tools v0.1.5 // indirect
	k8s.io/api v0.19.14
	k8s.io/apimachinery v0.19.14
	k8s.io/client-go v0.19.14
	k8s.io/code-generator v0.19.14
	k8s.io/klog v1.0.0
	k8s.io/kubectl v0.19.14
)

replace (
	golang.org/x/sys => golang.org/x/sys v0.0.0-20190813064441-fde4db37ae7a // pinned to release-branch.go1.13
	golang.org/x/tools => golang.org/x/tools v0.0.0-20190821162956-65e3620a7ae7 // pinned to release-branch.go1.13
)
