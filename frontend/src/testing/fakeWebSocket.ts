// NOTE: 测试专用的假 WebSocket：既可以经 createSocket 注入，也可以用 install() 替换全局 WebSocket（给 Pinia store 里内部创建的连接用）。
// 不会被应用代码引用，所以不进产物。
import { vi } from 'vitest'

export class FakeWebSocket {
  static instances: FakeWebSocket[] = []

  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  closed = false
  closeCode: number | undefined
  // NOTE: 与真实 WebSocket 一致：0 建立中，1 已打开，3 已关闭；控制器靠它判断唤醒时要不要重连
  readyState = 0

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this)
  }

  static get last(): FakeWebSocket {
    return FakeWebSocket.instances[FakeWebSocket.instances.length - 1]!
  }

  static install(): void {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  }

  close(code?: number): void {
    this.closed = true
    this.closeCode = code
    this.readyState = 3
  }

  // NOTE: frame 是字符串时原样发送（用来构造畸形帧），否则序列化成 JSON
  recv(frame: unknown): void {
    this.readyState = 1
    this.onmessage?.({ data: typeof frame === 'string' ? frame : JSON.stringify(frame) } as MessageEvent)
  }

  drop(): void {
    this.readyState = 3
    this.onclose?.()
  }
}
