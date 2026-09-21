package ident

import "errors"

var (
	ErrNonUniform = errors.New("数据非等间隔：未选择重采样时不能作为离散等步模型处理")
)
