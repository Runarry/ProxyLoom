import type { Schema } from '../api/client'

export const jobTypes: Record<Schema<'JobType'>, string> = { source_refresh: '来源刷新', import_parse: '导入解析', compile: '编译', config_validate: '配置校验', connectivity: '连通性', download_throughput: '下载测速' }
export const jobStates: Record<Schema<'JobState'>, string> = { queued: '排队中', leased: '准备执行', running: '执行中', succeeded: '执行完成', failed: '执行失败', canceled: '已取消', timed_out: '执行超时' }
export const phases: Record<string, string> = { queued: '等待槽位', leased: '已领取', preparing: '准备输入', validating: '校验配置', starting: '启动内核', probing: '探测目标', downloading: '限额下载', collecting: '收集结果', settling: '结算用量', canceling: '回收进程', completed: '已结束' }
export function terminal(state: string) { return ['succeeded', 'failed', 'canceled', 'timed_out'].includes(state) }
export function verdict(item: { state: string; verdict?: string }) {
  if (item.state !== 'succeeded') return jobStates[item.state as Schema<'JobState'>] ?? item.state
  return item.verdict === 'pass' ? '测试通过' : item.verdict === 'fail' ? '节点或链测试失败' : '无法判断'
}
export function bytes(value: number | undefined) { return value === undefined ? '未知' : value < 1024 ? `${value} B` : value < 1048576 ? `${(value / 1024).toFixed(1)} KiB` : `${(value / 1048576).toFixed(2)} MiB` }
export function duration(value: number | undefined) { return value === undefined ? '—' : `${value.toFixed(1)} ms` }
export function sampleSummary(metrics: Schema<'TestMetrics'>) { return metrics.sample_count === undefined ? '无可靠样本' : `有效 ${metrics.sample_count - (metrics.failure_count ?? 0)} / ${metrics.sample_count}，失败 ${metrics.failure_count ?? 0}` }
