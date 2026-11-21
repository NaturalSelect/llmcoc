import { describe, expect, test } from 'vitest'
import { createCompanionGenHandler, summarizeCompanionGen } from './companionGenerate'

function run(events: [string, string][]) {
  const lines: string[] = []
  const handler = createCompanionGenHandler((l) => lines.push(l))
  for (const [event, data] of events) handler.onEvent(event, data)
  return { lines, handler }
}

describe('createCompanionGenHandler', () => {
  test('开始、完成、失败事件转成日志行', () => {
    const { lines } = run([
      ['card_start', '{"index":1,"name":"陈守拙","occupation":"记者","gender":"男","age":31}'],
      ['card_done', '{"index":1,"card_id":9,"name":"陈守拙","occupation":"记者"}'],
      ['card_error', '{"index":2,"name":"苏闻笛","occupation":"医生","message":"AI生成失败: 模型超时"}'],
    ])
    expect(lines).toStrictEqual([
      '第 1 张开始生成：陈守拙（男，31 岁，记者）',
      '第 1 张完成：陈守拙（记者），已放入卡池',
      '第 2 张失败：苏闻笛，AI生成失败: 模型超时',
    ])
  })

  test('取名失败时还没有姓名：失败行不带姓名', () => {
    const { lines } = run([['card_error', '{"index":3,"message":"取名失败: 姓名池已用尽"}']])
    expect(lines).toStrictEqual(['第 3 张失败：取名失败: 姓名池已用尽'])
  })

  test('畸形 JSON 和未知事件被忽略，不影响后续事件', () => {
    const { lines, handler } = run([
      ['card_start', 'oops'],
      ['card_done', ''],
      ['card_error', '{'],
      ['progress', '{"detail":"x"}'],
      ['batch_done', '{"total":1,"succeeded":1,"failed":0}'],
    ])
    expect(lines).toStrictEqual([])
    expect(handler.batchDone).toStrictEqual({ total: 1, succeeded: 1, failed: 0 })
  })

  test('畸形 batch_done 清空汇总', () => {
    const { handler } = run([
      ['batch_done', '{"total":1,"succeeded":1,"failed":0}'],
      ['batch_done', 'oops'],
    ])
    expect(handler.batchDone).toBeNull()
  })

  test('snapshot：先通知页面，再按原时间回放历史事件，并记下汇总', () => {
    const order: string[] = []
    const lines: [string, number | undefined][] = []
    const handler = createCompanionGenHandler(
      (line, at) => {
        order.push('log')
        lines.push([line, at])
      },
      { onSnapshot: (snap) => order.push(`snapshot:${snap.running}`) },
    )
    handler.onEvent(
      'snapshot',
      JSON.stringify({
        running: false,
        events: [
          { type: 'card_start', data: { index: 1, name: '陈守拙', occupation: '记者', gender: '男', age: 31 }, at: 1000 },
          { type: 'card_error', data: { index: 1, name: '陈守拙', message: '模型超时' }, at: 2000 },
          { type: 'batch_done', data: { total: 1, succeeded: 0, failed: 1 }, at: 3000 },
        ],
      }),
    )
    expect(order).toStrictEqual(['snapshot:false', 'log', 'log'])
    expect(lines).toStrictEqual([
      ['第 1 张开始生成：陈守拙（男，31 岁，记者）', 1000],
      ['第 1 张失败：陈守拙，模型超时', 2000],
    ])
    expect(handler.batchDone).toStrictEqual({ total: 1, succeeded: 0, failed: 1 })
    expect(handler.snapshot?.running).toBe(false)
  })

  test('没有收到 snapshot 时 snapshot 为 null；畸形 snapshot 被忽略', () => {
    const { handler, lines } = run([['card_done', '{"index":1,"name":"陈守拙","occupation":"记者"}']])
    expect(handler.snapshot).toBeNull()
    expect(lines).toHaveLength(1)

    for (const bad of ['oops', 'null', '{"running":true}', '{"running":true,"events":null}']) {
      handler.onEvent('snapshot', bad)
    }
    expect(handler.snapshot).toBeNull()
    expect(lines).toHaveLength(1)
  })

  test('snapshot 里嵌套的 snapshot 不会递归处理', () => {
    const calls: boolean[] = []
    const handler = createCompanionGenHandler(() => {}, { onSnapshot: (s) => calls.push(s.running) })
    handler.onEvent('snapshot', JSON.stringify({ running: true, events: [{ type: 'snapshot', data: { running: false, events: [] } }] }))
    expect(calls).toStrictEqual([true])
  })
})

describe('summarizeCompanionGen', () => {
  test('全部成功：成功提示并刷新卡池', () => {
    const outcome = summarizeCompanionGen({ total: 3, succeeded: 3, failed: 0 })
    expect(outcome.toast).toStrictEqual({ message: '批量生成完成：成功 3 张，失败 0 张', type: 'success' })
    expect(outcome.reload).toBe(true)
    expect(outcome.logs).toStrictEqual(['批量生成完成：成功 3 张，失败 0 张'])
  })

  test('部分失败：错误提示，但有成功的仍刷新卡池', () => {
    const outcome = summarizeCompanionGen({ total: 3, succeeded: 2, failed: 1 })
    expect(outcome.toast.type).toBe('error')
    expect(outcome.reload).toBe(true)
  })

  test('全部失败：不刷新卡池', () => {
    const outcome = summarizeCompanionGen({ total: 2, succeeded: 0, failed: 2 })
    expect(outcome.toast.type).toBe('error')
    expect(outcome.reload).toBe(false)
  })

  test('没收到 batch_done：提示连接中断，不刷新', () => {
    const outcome = summarizeCompanionGen(null)
    expect(outcome.toast).toStrictEqual({ message: '连接中断，生成仍在后台继续', type: 'error' })
    expect(outcome.reload).toBe(false)
  })
})
