import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import PaginationBar from './PaginationBar.vue'

describe('PaginationBar', () => {
  it('supports keyboard page navigation and rejects out-of-range jumps', async () => {
    const user = userEvent.setup()
    const update = vi.fn()
    render(PaginationBar, {
      props: { page: 1, pageSize: 20, total: 45, count: 20, 'onUpdate:page': update },
    })
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(update).toHaveBeenLastCalledWith(2)
    const target = screen.getByRole('textbox', { name: '跳转页码' })
    await user.clear(target)
    await user.type(target, '4')
    expect(screen.getByRole('button', { name: /^跳转$/ })).toBeDisabled()
    await user.clear(target)
    await user.type(target, '3{Enter}')
    expect(update).toHaveBeenLastCalledWith(3)
  })
})
