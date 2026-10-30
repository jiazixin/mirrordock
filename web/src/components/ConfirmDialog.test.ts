import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import ConfirmDialog from './ConfirmDialog.vue'

describe('ConfirmDialog', () => {
  it('starts a destructive confirmation on its safe action and restores the trigger', async () => {
    const trigger = document.createElement('button')
    const container = document.createElement('div')
    document.body.append(trigger, container)
    trigger.focus()
    const settle = vi.fn()
    const view = render(ConfirmDialog, {
      container,
      props: {
        title: '取消任务',
        message: '确定取消？',
        confirmLabel: '取消任务',
        cancelLabel: '保留任务',
        tone: 'red',
        onSettle: settle,
      },
    })
    await waitFor(() => expect(screen.getByRole('button', { name: '保留任务' })).toHaveFocus())
    expect(screen.queryByRole('button', { name: '关闭' })).not.toBeInTheDocument()
    await userEvent.setup().keyboard('{Escape}')
    expect(settle).toHaveBeenCalledWith(false)
    view.unmount()
    expect(trigger).toHaveFocus()
    trigger.remove()
    container.remove()
  })
})
