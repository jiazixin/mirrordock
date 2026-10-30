import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import ModalDialog from './ModalDialog.vue'

describe('ModalDialog', () => {
  it('moves focus into the dialog, handles Escape, and restores focus', async () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const close = vi.fn()
    const container = document.createElement('div')
    document.body.append(container)
    const view = render(ModalDialog, { container, props: { title: '任务详情', onClose: close } })
    await waitFor(() => expect(screen.getByRole('dialog', { name: '任务详情' })).toHaveFocus())
    await userEvent.setup().keyboard('{Escape}')
    expect(close).toHaveBeenCalledOnce()
    view.unmount()
    expect(trigger).toHaveFocus()
    trigger.remove()
    container.remove()
  })
})
