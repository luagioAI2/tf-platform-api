// Package buildinfo 提供构建期注入的版本与环境信息。
//
// 单独成包，避免 api / router / internal 之间因共享一个 string 而产生循环依赖，
// 同时让 wire 能唯一识别（两个裸 string 参数会冲突）。
package buildinfo

// Info 构建信息
type Info struct {
	Version string
	Env     string
}

// IsProduction 是否生产环境
func (i Info) IsProduction() bool { return i.Env == "production" }
