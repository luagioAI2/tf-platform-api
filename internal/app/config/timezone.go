package config

import "time"

// loadLocation 包一层 time.LoadLocation，便于在 config 包内做校验而不引入循环依赖
func loadLocation(name string) (*time.Location, error) {
	return time.LoadLocation(name)
}
