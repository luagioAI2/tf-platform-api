//go:build wireinject
// +build wireinject

package internal

import (
	"tf-api/internal/app/api"
	"tf-api/internal/app/buildinfo"
	"tf-api/internal/app/router"
	"tf-api/internal/app/service"
	"tf-api/internal/app/service/repository"

	"github.com/google/wire"
)

// BuildInjector 由 wire 生成实现（见 wire_gen.go）。
// 修改依赖后必须执行：make wire
//
// info 作为注入器入参，wire 会把它当作已提供的依赖。
func BuildInjector(info buildinfo.Info) (*Injector, func(), error) {
	wire.Build(
		ProviderSet,
		repository.RepoSet,
		service.SrvSet,
		api.APISet,
		router.RouterSet,
		InitGinEngine,
		InjectorSet,
	)
	return new(Injector), nil, nil
}
