/** One stacked series of TrendBars. */
export interface TrendSeries {
  key: string
  name: string
  color: string // CSS colour, e.g. var(--viz-cat-1)
  values: number[] // one per bucket
}

/** A node of FlowChart; column 0 is leftmost. */
export interface FlowNode {
  id: string
  name: string
  column: number
  color: string
  selectable?: boolean // clicking it emits select
  lit?: boolean // on a path through the focused node
}

/** A FlowChart link from a node to one in a later column. */
export interface FlowLink {
  source: string
  target: string
  value: number
  color: string
  to?: string // target-end colour: the link fades from color to it
  lit?: boolean // on a path through the focused node
}
