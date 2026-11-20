//go:build !windows

package core

import "errors"

type systemCipher struct{}

func NewSystemCipher() Cipher { return systemCipher{} }
func (systemCipher) Encrypt([]byte) (string, error) {
	return "", errors.New("当前平台尚未配置系统安全存储，请取消记住凭据")
}
func (systemCipher) Decrypt(string) ([]byte, error) {
	return nil, errors.New("当前平台尚未配置系统安全存储，请重新输入凭据")
}
