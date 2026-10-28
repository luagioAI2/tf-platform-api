package async

// IAsyncService 异步任务门面接口。
//
// mq/handle 与 task 只依赖本接口，新增能力时同步在此声明方法。
type IAsyncService interface {
	// Noop 占位方法，接入业务后替换为真实能力
	Noop() error
}
