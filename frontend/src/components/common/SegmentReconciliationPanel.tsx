import { Alert, Table, Tag } from 'antd'
import { AlertTriangle, CheckCircle2 } from 'lucide-react'
import type { BalanceRun, BalanceSegment } from '../../types/balance'
import { dateTime, kg } from '../../utils/format'

export function SegmentReconciliationPanel({ run }: { run?: BalanceRun }) {
  const segments = run?.evidence_json?.segments
  if (!run || !segments || segments.items.length === 0) return null
  const unexplained = segments.items.filter((item) => item.exceeds_uncertainty)
  return (
    <section className="evidence-panel" aria-label="期内分段核对">
      <div className="section-heading">
        <div>
          <span className="eyebrow">INTRA-PERIOD SEGMENTS</span>
          <h2>期内分段核对</h2>
          <span>{segments.snapshot_count} 条有效快照 · {segments.segment_count} 个相邻时段</span>
        </div>
        <Tag color={segments.unexplained_count > 0 ? 'warning' : 'success'}>
          {segments.unexplained_count > 0 ? `${segments.unexplained_count} 段未解释` : '各段均在不确定度内'}
        </Tag>
      </div>
      {unexplained.length > 0 && (
        <Alert
          className="segment-alert"
          type="warning"
          showIcon
          icon={<AlertTriangle size={18} />}
          message="以下时段的快照质量变化与已确认转移净量对不上，差值超过该段合成不确定度，请复核是否漏记或错记转移："
          description={unexplained.map((item) => (
            <div key={item.index} className="unexplained-line">
              <strong>第 {item.index} 段</strong>
              <span>{dateTime(item.start_at)} → {dateTime(item.end_at)}</span>
              <span>未解释 {kg(item.unexplained_kg)}</span>
              <span className="secondary">合成不确定度 ±{kg(item.uncertainty_kg)}</span>
            </div>
          ))}
        />
      )}
      <Table<BalanceSegment>
        className="evidence-table"
        rowKey={(item) => `segment-${item.index}`}
        size="small"
        pagination={false}
        dataSource={segments.items}
        rowClassName={(item) => (item.exceeds_uncertainty ? 'unexplained-row' : '')}
        columns={[
          {
            title: '时段',
            key: 'index',
            width: 72,
            render: (_, item) => <strong>第 {item.index} 段</strong>
          },
          { title: '起始时刻', dataIndex: 'start_at', width: 124, render: dateTime },
          { title: '结束时刻', dataIndex: 'end_at', width: 124, render: dateTime },
          {
            title: '快照质量变化',
            dataIndex: 'mass_change_kg',
            align: 'right',
            render: (value: number) => kg(value)
          },
          {
            title: '确认转移净量',
            dataIndex: 'net_transfer_kg',
            align: 'right',
            render: (value: number, record) => `${kg(value)}（${record.transfer_count} 笔）`
          },
          {
            title: '未解释差值',
            dataIndex: 'unexplained_kg',
            align: 'right',
            render: (value: number) => <strong>{kg(value)}</strong>
          },
          {
            title: '合成不确定度',
            dataIndex: 'uncertainty_kg',
            align: 'right',
            render: (value: number) => `±${kg(value)}`
          },
          {
            title: '结论',
            key: 'result',
            width: 120,
            render: (_, item) => item.exceeds_uncertainty
              ? <Tag color="warning" icon={<AlertTriangle size={12} />}>未解释</Tag>
              : <Tag color="success" icon={<CheckCircle2 size={12} />}>在不确定度内</Tag>
          }
        ]}
      />
      {segments.unallocated_transfers?.length ? (
        <Alert
          type="info"
          showIcon
          message={`${segments.unallocated_transfers.length} 笔已确认转移跨越快照边界或位于快照链之外，不能唯一归入某一段，未计入分段净量`}
          description={segments.unallocated_transfers.map((transfer) => (
            <div key={transfer.transfer_id} className="unexplained-line">
              <strong>转移 #{transfer.transfer_id}</strong>
              <span>{transfer.reason}</span>
            </div>
          ))}
        />
      ) : null}
      <Alert type="warning" showIcon message={segments.note} />
    </section>
  )
}
