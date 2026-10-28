package auth

import (
	"context"
	"net/http"
)

// 「这个请求是不是从远程（中继隧道）进来的」—— 本机口和远程口的规则完全不同，全压在这一个判据上。
//
// 判据只能是**落在哪个监听上**，绝不能看源 IP 或 Host：隧道里的源地址是中继说的（它被拿下时
// 想填 127.0.0.1 就填 127.0.0.1），Host 是客户端自己写的。看错一处，等于那道门不存在。

type remoteKey struct{}

// MarkRemote 盖一个「从中继隧道进来的」的章。只有 server.RemoteHandler 会调它。
func MarkRemote(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), remoteKey{}, true))
}

// FromRemote 这个请求是不是落在远程那个监听上。
func FromRemote(r *http.Request) bool {
	v, _ := r.Context().Value(remoteKey{}).(bool)
	return v
}
