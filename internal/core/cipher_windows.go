//go:build windows

package core

import (
	"encoding/base64"
	"errors"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

type systemCipher struct{}
type dataBlob struct {
	size uint32
	data *byte
}

var crypt32 = syscall.NewLazyDLL("crypt32.dll")
var protectData = crypt32.NewProc("CryptProtectData")
var unprotectData = crypt32.NewProc("CryptUnprotectData")
var localFree = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")

const cipherPrefix = "wails-dpapi-v1:"

func NewSystemCipher() Cipher { return systemCipher{} }
func blobFor(data []byte) dataBlob {
	if len(data) == 0 {
		return dataBlob{}
	}
	return dataBlob{size: uint32(len(data)), data: &data[0]}
}
func (c systemCipher) Encrypt(value []byte) (string, error) {
	if len(value) == 0 || len(value) > 65536 {
		return "", errors.New("凭据长度不正确")
	}
	input := blobFor(value)
	entropyBytes := []byte("SSHTool/v0.3/credentials")
	entropy := blobFor(entropyBytes)
	var output dataBlob
	ok, _, _ := protectData.Call(uintptr(unsafe.Pointer(&input)), 0, uintptr(unsafe.Pointer(&entropy)), 0, 0, 1, uintptr(unsafe.Pointer(&output)))
	runtime.KeepAlive(value)
	runtime.KeepAlive(entropyBytes)
	if ok == 0 {
		return "", errors.New("系统安全存储不可用")
	}
	defer localFree.Call(uintptr(unsafe.Pointer(output.data)))
	if output.size == 0 || output.size > 1024*1024 {
		return "", errors.New("系统安全存储返回无效数据")
	}
	return cipherPrefix + base64.StdEncoding.EncodeToString(unsafe.Slice(output.data, int(output.size))), nil
}
func (c systemCipher) Decrypt(value string) ([]byte, error) {
	if !strings.HasPrefix(value, cipherPrefix) || len(value) > 256*1024 {
		return nil, errors.New("不支持的凭据格式，请重新输入")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, cipherPrefix))
	if err != nil || len(data) == 0 {
		return nil, errors.New("保存的凭据已损坏")
	}
	defer clear(data)
	input := blobFor(data)
	entropyBytes := []byte("SSHTool/v0.3/credentials")
	entropy := blobFor(entropyBytes)
	var output dataBlob
	ok, _, _ := unprotectData.Call(uintptr(unsafe.Pointer(&input)), 0, uintptr(unsafe.Pointer(&entropy)), 0, 0, 1, uintptr(unsafe.Pointer(&output)))
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropyBytes)
	if ok == 0 {
		return nil, errors.New("系统无法解密保存的凭据，请重新输入")
	}
	defer localFree.Call(uintptr(unsafe.Pointer(output.data)))
	if output.size == 0 || output.size > 65536 {
		return nil, errors.New("系统安全存储返回无效数据")
	}
	plain := append([]byte(nil), unsafe.Slice(output.data, int(output.size))...)
	clear(unsafe.Slice(output.data, int(output.size)))
	return plain, nil
}
