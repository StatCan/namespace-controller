// This is a generated file. Do not edit directly.

module github.com/StatCan/namespace-controller

go 1.13

require (
	github.com/golang/protobuf v1.3.5 // indirect
	github.com/json-iterator/go v1.1.10 // indirect
	github.com/spf13/cobra v1.1.3
	golang.org/x/crypto v0.0.0-20200709230013-948cd5f35899 // indirect
	golang.org/x/net v0.0.0-20200707034311-ab3426394381 // indirect
	golang.org/x/sys v0.0.0-20200625212154-ddb9806d33ae // indirect
	golang.org/x/text v0.3.3 // indirect
	golang.org/x/tools v0.1.5 // indirect
	k8s.io/api v0.0.0-20200403220253-fa879b399cd0
	k8s.io/apimachinery v0.18.1
	k8s.io/client-go v0.18.1
	k8s.io/code-generator v0.0.0-20200403215918-804a58607501
	k8s.io/klog v1.0.0
)

replace (
	golang.org/x/sys => golang.org/x/sys v0.0.0-20190813064441-fde4db37ae7a // pinned to release-branch.go1.13
	golang.org/x/tools => golang.org/x/tools v0.0.0-20190821162956-65e3620a7ae7 // pinned to release-branch.go1.13
	k8s.io/api => k8s.io/api v0.0.0-20200403220253-fa879b399cd0
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20200403220105-fa0d5bf06730
	k8s.io/client-go => k8s.io/client-go v0.0.0-20200403220520-7039b495eb3e
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20200403215918-804a58607501
)
