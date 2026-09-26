import { Alert, Descriptions, Table, Tag } from 'antd'
import type { BalanceRun, BalanceSegment, UncertaintyComponent } from '../../types/balance'
import { deviationLabels } from '../../types/deviation'
import { dateTime, kg, number } from '../../utils/format'

const sourceLabels: Record<string, string> = {
  opening_snapshot: '期初快照',
  closing_snapshot: '期末快照',
  transfer_inflow: '流入计量',
  transfer_outflow: '流出计量',
  segment_opening_snapshot: '分段期初快照',
  segment_closing_snapshot: '分段期末快照'
}

function segmentVerdict(segment: BalanceSegment) {
  if (segment.unexplained) {
    const color = segment.level === 'investigate' ? 'warning' : 'warning'
    return <Tag color={color}>未解释区间 · {deviationLabels[segment.level]}</Tag>
  }
  return <Tag color="success">不确定度内</Tag>
}

export function EvidenceBreakdownPanel({ run }: { run?: BalanceRun }) {
  if (!run) return null
  const evidence = run.evidence_json ?? {}
  const uncertainty = evidence.uncertainty
  const segments = evidence.segments ?? []
  const unexplained = evidence.unexplained_intervals ?? []
  return (
    <section className="evidence-panel" aria-label="平衡证据明细">
      <div className="section-heading">
        <div>
          <span className="eyebrow">EVIDENCE SNAPSHOT</span>
          <h2>证据与不确定度</h2>
        </div>
        <Tag color={run.deviation_level === 'investigate' ? 'warning' : 'success'}>
          {deviationLabels[run.deviation_level]}
        </Tag>
      </div>
      <Descriptions size="small" column={{ xs: 1, sm: 2, lg: 4 }} bordered>
        <Descriptions.Item label="算法版本">{evidence.algorithm_version ?? 'mass-balance-v1.0'}</Descriptions.Item>
        <Descriptions.Item label="系数版本">{run.coefficient_version}</Descriptions.Item>
        <Descriptions.Item label="合成不确定度">{kg(run.uncertainty_kg)}</Descriptions.Item>
        <Descriptions.Item label="偏差">{number.format(run.deviation_pct)}%</Descriptions.Item>
      </Descriptions>
      {segments.length ? (
        <div className="segment-reconciliation">
          <div className="section-heading">
            <div><h3>期内分段复核</h3><span>{segments.length} 个相邻快照区间</span></div>
            <Tag color={unexplained.length ? 'warning' : 'success'}>{unexplained.length} 个未解释区间</Tag>
          </div>
          {unexplained.length ? (
            <Alert
              type="warning"
              showIcon
              message={`检测到 ${unexplained.length} 个未解释区间：区间质量变化与确认转移净量的差值超过该段合成不确定度`}
              description={
                <ul className="unexplained-list">
                  {unexplained.map((segment) => (
                    <li key={segment.sequence}>
                      第 {segment.sequence} 段 {dateTime(segment.start_at)} 至 {dateTime(segment.end_at)}
                      （快照 #{segment.opening_snapshot_id} → #{segment.closing_snapshot_id}）：
                      差值 <strong>{kg(segment.discrepancy_kg)}</strong>，
                      合成不确定度 ±{kg(segment.uncertainty_kg)}
                    </li>
                  ))}
                </ul>
              }
            />
          ) : (
            <Alert type="success" showIcon message="全部分段差值均在各自合成不确定度内，期内无未解释区间。" />
          )}
          <Table<BalanceSegment>
            className="evidence-table"
            rowKey={(item) => item.start_at + '-' + item.end_at}
            size="small"
            pagination={false}
            dataSource={segments}
            rowClassName={(segment) => segment.unexplained ? 'unexplained-row' : ''}
            columns={[
              { title: '段', dataIndex: 'sequence', width: 56 },
              {
                title: '区间起止', key: 'window',
                render: (_, item) => <><div>{dateTime(item.start_at)}</div><div className="secondary">至 {dateTime(item.end_at)}</div></>
              },
              { title: '期初质量', dataIndex: 'opening_mass_kg', align: 'right', render: kg },
              { title: '期末质量', dataIndex: 'closing_mass_kg', align: 'right', render: kg },
              {
                title: '确认转移净量', dataIndex: 'net_transfer_kg', align: 'right',
                render: (value: number) => <span className={value < 0 ? 'negative-mass' : ''}>{kg(value)}</span>
              },
              {
                title: '差值', dataIndex: 'discrepancy_kg', align: 'right',
                render: (value: number) => <strong className={Math.abs(value) > 0 ? 'mass-delta' : ''}>{kg(value)}</strong>
              },
              { title: '合成不确定度', dataIndex: 'uncertainty_kg', align: 'right', render: (value: number) => '± ' + kg(value) },
              { title: '判定', key: 'verdict', width: 170, render: (_, item) => segmentVerdict(item) }
            ]}
          />
          {evidence.segment_attribution && <p className="secondary segment-rule">{evidence.segment_attribution}</p>}
          {evidence.transfers_outside_chain?.length ? (
            <Alert
              type="info"
              showIcon
              message={`${evidence.transfers_outside_chain.length} 条确认转移开始于期末快照之后，计入整段平衡但未归属到任何分段：#${evidence.transfers_outside_chain.join('、#')}`}
            />
          ) : null}
        </div>
      ) : null}
      {uncertainty?.components?.length ? (
        <Table<UncertaintyComponent>
          className="evidence-table"
          rowKey={(item) => item.source + item.entity_id}
          size="small"
          pagination={false}
          dataSource={uncertainty.components}
          columns={[
            { title: '来源', dataIndex: 'source', render: (value: string) => sourceLabels[value] ?? value },
            { title: '证据 ID', dataIndex: 'entity_id' },
            { title: '质量', dataIndex: 'mass_kg', align: 'right', render: kg },
            { title: '不确定度', dataIndex: 'uncertainty_pct', align: 'right', render: (value: number) => number.format(value) + '%' },
            { title: '绝对贡献', dataIndex: 'absolute_kg', align: 'right', render: kg }
          ]}
        />
      ) : null}
      <Alert type="warning" showIcon message={evidence.safety_boundary ?? '未解释差异仅作工程分析，不直接认定为泄漏或安全事件。'} />
    </section>
  )
}
