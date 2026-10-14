import { defineComponent, h, type PropType, type VNodeChild } from 'vue'
import { NEmpty } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'

type Column = {
  key?: string | number
  title?: unknown
  render?: (row: never, index: number) => VNodeChild
}

/**
 * The phone layout of a data table: one card per row, built from the same
 * column definitions. The title column and the actions column head the card;
 * every other column becomes a label/value line.
 */
export default defineComponent({
  name: 'MobileCards',
  props: {
    columns: { type: Array as PropType<DataTableColumns<never>>, required: true },
    data: { type: Array as PropType<unknown[]>, required: true },
    rowKey: { type: Function as PropType<(row: never) => string | number>, required: true },
    titleKey: { type: String, required: true },
    actionsKey: { type: String, default: '' },
    hide: { type: Array as PropType<string[]>, default: () => [] },
    empty: { type: String, default: '' },
    onRowClick: { type: Function as PropType<(row: never) => void>, default: undefined },
  },
  setup(props) {
    const cell = (col: Column, row: unknown, i: number): VNodeChild => {
      if (col.render) return col.render(row as never, i)
      const v = (row as Record<string, unknown>)[String(col.key)]
      return v === undefined || v === null || v === '' ? '—' : String(v)
    }
    const label = (col: Column) => (typeof col.title === 'function' ? (col.title as () => VNodeChild)() : (col.title as string))

    return () => {
      const cols = props.columns as Column[]
      if (!props.data.length) return h(NEmpty, { description: props.empty, class: 'mc-empty' })
      const title = cols.find((c) => c.key === props.titleKey)
      const actions = cols.find((c) => c.key === props.actionsKey)
      const rest = cols.filter((c) => c !== title && c !== actions && !props.hide.includes(String(c.key)))
      return h(
        'div',
        { class: 'mc-list' },
        props.data.map((row, i) =>
          h(
            'div',
            {
              key: props.rowKey(row as never),
              class: ['mc-card', props.onRowClick && 'mc-click'],
              onClick: props.onRowClick ? () => props.onRowClick!(row as never) : undefined,
            },
            [
              h('div', { class: 'mc-head' }, [
                h('div', { class: 'mc-title' }, title ? [cell(title, row, i)] : []),
                actions ? h('div', { class: 'mc-actions', onClick: (e: Event) => e.stopPropagation() }, [cell(actions, row, i)]) : null,
              ]),
              rest.length
                ? h(
                    'div',
                    { class: 'mc-fields' },
                    rest.map((c) =>
                      h('div', { class: 'mc-field', key: String(c.key) }, [
                        h('div', { class: 'mc-label' }, [label(c)]),
                        h('div', { class: 'mc-value' }, [cell(c, row, i)]),
                      ]),
                    ),
                  )
                : null,
            ],
          ),
        ),
      )
    }
  },
})
