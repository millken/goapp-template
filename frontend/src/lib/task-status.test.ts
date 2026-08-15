import { describe, expect, it } from 'vitest'
import {
  attemptOutcomeMeta,
  scheduleStatusMeta,
  TASK_STATUSES,
  taskStatusMeta,
} from './task-status'

describe('taskStatusMeta', () => {
  it('describes every status the queue stores', () => {
    // A guard against adding a status server-side and leaving it to fall through to
    // the raw-string fallback on three screens.
    for (const status of TASK_STATUSES) {
      const meta = taskStatusMeta(status)
      expect(meta.label, status).not.toBe(status)
      expect(meta.label, status).not.toBe('')
    }
  })

  // A pending task that has already failed is waiting out a backoff. Calling that
  // "待执行" next to a task that has never run hides the thing being looked for.
  it('separates a first-time pending task from one waiting to retry', () => {
    const fresh = taskStatusMeta('pending', 0, 3)
    expect(fresh.label).toBe('待执行')
    expect(fresh.class).toBeUndefined()

    const retrying = taskStatusMeta('pending', 2, 5)
    expect(retrying.label).toBe('重试等待 2/5')
    // Destructive text but an outline badge: it is a problem, but a milder one than
    // 'dead' — it is still going to retry itself.
    expect(retrying.class).toContain('destructive')
    expect(retrying.variant).toBe('outline')
  })

  // These two must not look alike. One retries itself; the other needs a person.
  it('makes retry-waiting and dead visually distinct', () => {
    const retrying = taskStatusMeta('pending', 2, 5)
    const dead = taskStatusMeta('dead')
    expect(dead.variant).toBe('destructive')
    expect(retrying.variant).not.toBe(dead.variant)
  })

  it('uses the success variant for a finished task', () => {
    // Which is the whole reason badge carries a local success variant.
    expect(taskStatusMeta('succeeded').variant).toBe('success')
  })

  it('marks the only live status with the primary colour', () => {
    // Free on this page: the task list deliberately has no primary action.
    expect(taskStatusMeta('running').variant).toBe('default')
  })

  // A migration that adds a status must not blank the page for an older frontend.
  it('falls back to showing an unknown status as itself', () => {
    const meta = taskStatusMeta('archived')
    expect(meta.label).toBe('archived')
    expect(meta.variant).toBe('outline')
  })
})

describe('attemptOutcomeMeta', () => {
  it('describes every outcome the queue records', () => {
    for (const outcome of ['succeeded', 'failed', 'timeout', 'lost', 'cancelled']) {
      expect(attemptOutcomeMeta(outcome).label, outcome).not.toBe(outcome)
    }
  })

  // A slow dependency and a broken one call for different responses, and the error text
  // does not distinguish them — so the outcome has to.
  it('distinguishes a timeout and a lost worker from a plain failure', () => {
    expect(attemptOutcomeMeta('timeout').label).not.toBe(attemptOutcomeMeta('failed').label)
    expect(attemptOutcomeMeta('lost').label).not.toBe(attemptOutcomeMeta('failed').label)
  })

  it('falls back to showing an unknown outcome as itself', () => {
    expect(attemptOutcomeMeta('exploded').label).toBe('exploded')
  })
})

describe('scheduleStatusMeta', () => {
  const plan = (over: Partial<Parameters<typeof scheduleStatusMeta>[0]> = {}) =>
    scheduleStatusMeta({ enabled: true, present: true, known_kind: true, ...over })

  it('reports a live plan as enabled', () => {
    expect(plan().label).toBe('已启用')
    expect(plan().variant).toBe('success')
  })

  it('reports a paused plan', () => {
    expect(plan({ enabled: false }).label).toBe('已暂停')
  })

  // These are not one axis. A plan the code has dropped will not fire however enabled
  // it looks, so that has to be what is shown.
  it('ranks removed-from-the-code above paused', () => {
    expect(plan({ present: false, enabled: true }).label).toBe('代码中已移除')
    expect(plan({ present: false, enabled: false }).label).toBe('代码中已移除')
  })

  it('ranks a missing handler above enabled', () => {
    expect(plan({ known_kind: false }).label).toBe('无处理器')
  })
})
