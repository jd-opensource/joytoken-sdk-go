package joytokenexample

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	agent "github.com/jd-opensource/joytoken-sdk-go/agent"
	toolkit "github.com/jd-opensource/joytoken-sdk-go/agent/toolkit"
)

// ---------------------------------------------------------------------------
// 形态 A：CLI —— 从 stdin 读取 y/n 让用户逐次审批
// ---------------------------------------------------------------------------
//
// 适合本地脚本 / CLI 工具。PermissionFunc 里直接向终端打印待审批的工具调用，
// 再从标准输入读一行 y/n。返回 true 放行、false 拒绝。
//
// 这里用 in/out 参数注入输入输出，方便测试；真实程序传 os.Stdin / os.Stdout。
func newCLIPermission(in io.Reader, out io.Writer) toolkit.Permission {
	reader := bufio.NewReader(in)
	ask := func(_ context.Context, req toolkit.PermissionRequest) (bool, error) {
		fmt.Fprintf(out, "工具 %q 请求执行(step %d)，参数：%v\n是否允许？[y/N] ",
			req.ToolName, req.Step, req.Input)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
	return toolkit.Permission{Mode: toolkit.PermissionAsk, Ask: ask}
}

// ExamplePermission_cli 演示 CLI 交互式审批：用户输入 y 放行 file_write。
func ExamplePermission_cli() {
	// 用户输入 "y" 表示同意。
	perm := newCLIPermission(strings.NewReader("y\n"), io.Discard)

	// 把 Ask 策略挂到 Toolkit，注册一个有副作用的写文件工具。
	tk := toolkit.New(toolkit.WithPermission(perm)).
		Register(toolkit.FileWrite(toolkit.FileSandbox{Root: "."}))

	// tools 已被权限中间件包裹，取出后交给 agent。
	tools := tk.Tools()
	fmt.Printf("注册工具数：%d，首个工具：%s\n", len(tools), tools[0].Name)

	// 直接调用被包裹的 Execute，模拟 agent 循环中的一次工具调用。
	// 因为审批回调返回 y，执行会通过（此处不真正落盘，仅演示放行路径）。
	_, err := tools[0].Execute(context.Background(),
		map[string]any{"path": "demo.txt", "content": "hi"},
		agent.ToolExecutionContext{Step: 1})
	fmt.Printf("执行是否被权限拒绝：%v\n", err != nil)

	// Output:
	// 注册工具数：1，首个工具：file_write
	// 执行是否被权限拒绝：false
}

// ---------------------------------------------------------------------------
// 形态 B：GUI/Web —— 回调对接前端审批流（推送 + 等待确认）
// ---------------------------------------------------------------------------
//
// 适合 GUI / Web 服务。PermissionFunc 不阻塞式读终端，而是：
//  1. 把待审批请求推给前端（下例用一个 channel 模拟"推送到前端")；
//  2. 阻塞等待前端把用户的决定回传（用另一个 channel 模拟"用户点了同意/拒绝")；
//  3. 全程尊重 ctx：用户迟迟不点、或请求被取消时，及时返回而不是卡死。
type approvalRequest struct {
	req   toolkit.PermissionRequest
	reply chan bool
}

func newWebPermission(pending chan<- approvalRequest) toolkit.Permission {
	ask := func(ctx context.Context, req toolkit.PermissionRequest) (bool, error) {
		reply := make(chan bool, 1)
		// 推送到前端（真实场景是 WebSocket / SSE / 消息队列）。
		select {
		case pending <- approvalRequest{req: req, reply: reply}:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		// 等待前端回传用户决定。
		select {
		case allow := <-reply:
			return allow, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return toolkit.Permission{Mode: toolkit.PermissionAsk, Ask: ask}
}

// ExamplePermission_web 演示 Web 审批：后端把请求推给"前端"，前端点同意后放行。
func ExamplePermission_web() {
	pending := make(chan approvalRequest, 1)

	// 模拟前端：收到审批请求后，用户点了"同意"。
	go func() {
		ar := <-pending
		fmt.Printf("前端收到审批：工具=%s\n", ar.req.ToolName)
		ar.reply <- true // 用户点同意
	}()

	perm := newWebPermission(pending)
	tk := toolkit.New(toolkit.WithPermission(perm)).
		Register(toolkit.FileWrite(toolkit.FileSandbox{Root: "."}))
	tools := tk.Tools()

	_, err := tools[0].Execute(context.Background(),
		map[string]any{"path": "demo.txt", "content": "hi"},
		agent.ToolExecutionContext{Step: 1})
	fmt.Printf("执行是否被权限拒绝：%v\n", err != nil)

	// Output:
	// 前端收到审批：工具=file_write
	// 执行是否被权限拒绝：false
}

// ---------------------------------------------------------------------------
// fail-safe：Ask 档但没配回调 —— 一律拒绝，绝不静默放行
// ---------------------------------------------------------------------------
func ExamplePermission_failSafe() {
	// Mode=Ask 但 Ask 回调为 nil。
	tk := toolkit.New(toolkit.WithPermission(toolkit.Permission{Mode: toolkit.PermissionAsk})).
		Register(toolkit.Calculator())
	tools := tk.Tools()

	_, err := tools[0].Execute(context.Background(),
		map[string]any{"expression": "1+1"},
		agent.ToolExecutionContext{Step: 1})
	fmt.Printf("无回调的 Ask 是否被拒绝：%v\n", err != nil)

	// Output:
	// 无回调的 Ask 是否被拒绝：true
}