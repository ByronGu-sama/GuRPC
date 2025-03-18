package codec

type Header struct {
	ServiceMethod string // 服务名/方法名
	Seq           uint64 // 请求序号
	Error         string // 错误消息
}
