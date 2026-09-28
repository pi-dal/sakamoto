package svc

import (
	"fmt"
	"net"
	"time"
)

// Send 向 svc socket 发送单条命令并返回响应。
func Send(cmd string) (string, error) {
	conn, err := net.DialTimeout("unix", SockPath(), 3*time.Second)
	if err != nil {
		return "", fmt.Errorf("svc 未运行（先用 sudo 起 LaunchDaemon）: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintln(conn, cmd); err != nil {
		return "", err
	}
	buf := make([]byte, 64<<10)
	n, err := conn.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buf[:n]), nil
}
