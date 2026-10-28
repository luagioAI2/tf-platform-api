package rds

// 本文件保留为空占位：Redis 客户端的初始化与访问已统一收敛到
// distributed_lock.go 中的 SetClient / GetClient / InitRDS，
// 以保证全局客户端只有一处读写点（带 RWMutex 保护）。
//
// 新增 Redis 相关工具函数请放到独立文件（如 ratelimit.go / cache_adapter.go），
// 不要在这里重复声明客户端变量。
