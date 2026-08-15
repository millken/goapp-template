// How a task's status is shown: one mapping, used by the list, the detail page and
// the schedule list.
//
// It lives here rather than in each page because three screens rendering the same
// status three slightly different ways is how an operator learns to distrust the
// colours. Same reasoning as lib/media-url.ts mirroring the Go escaping in one place.

export type BadgeVariant = 'default' | 'secondary' | 'destructive' | 'outline' | 'success'

export type StatusMeta = {
  label: string
  variant: BadgeVariant
  /** Extra classes, for the one status a variant alone cannot express. */
  class?: string
}

// The statuses the queue stores. There is deliberately no 'failed': a failure that
// will be retried goes back to 'pending', so 'dead' is the only terminal failure.
export const TASK_STATUSES = [
  'pending',
  'running',
  'succeeded',
  'dead',
  'cancelled',
] as const

/**
 * taskStatusMeta describes how to render one status.
 *
 * attempts and max are used for the one case the status alone cannot express: a
 * pending task that has already failed at least once is waiting out a backoff, and
 * calling that "待执行" alongside a task that has never run hides the thing an
 * operator is looking for. It gets its own label and a destructive text colour —
 * lighter than 'dead', because it is still going to retry itself.
 *
 * An unknown status falls back to showing itself in an outline badge. A migration
 * that adds a status must not blank the page for anyone running an older frontend.
 */
export function taskStatusMeta(status: string, attempts = 0, max = 0): StatusMeta {
  switch (status) {
    case 'pending':
      if (attempts > 0) {
        return {
          label: `重试等待 ${attempts}/${max}`,
          variant: 'outline',
          class: 'border-destructive/40 text-destructive',
        }
      }
      return { label: '待执行', variant: 'outline' }

    case 'running':
      // primary, which the design otherwise reserves for a page's main action — but
      // the task list has no main action (there is no "new task" button, by design),
      // so it is free here, and "the only one that is alive" is exactly what it
      // should mark.
      return { label: '执行中', variant: 'default' }

    case 'succeeded':
      return { label: '成功', variant: 'success' }

    case 'dead':
      // Solid destructive, the loudest thing available: this is the only status that
      // needs a person. It must not look like the retry-waiting state above.
      return { label: '已失败（重试用尽）', variant: 'destructive' }

    case 'cancelled':
      return { label: '已取消', variant: 'secondary' }

    default:
      return { label: status, variant: 'outline' }
  }
}

/** Which per-task actions a status allows. */
export type TaskActions = {
  retry: boolean
  run: boolean
  cancel: boolean
  delete: boolean
}

/**
 * taskActions reports which actions a task in this status offers.
 *
 * Hiding a button is convenience only — the real guard is the `WHERE status IN (...)`
 * of each action in the queue service, so a stale page cannot do anything it should
 * not. But the convenience copy belongs here rather than in each page: the list and
 * the detail page both need it, and two copies mean the same task can offer 重试 on
 * one screen and not the other, with nothing that fails when they disagree.
 *
 * Mirrors internal/service/queue/actions.go. A relaxed WHERE clause there is the one
 * thing that has to be echoed here.
 */
export function taskActions(status: string): TaskActions {
  return {
    retry: ['succeeded', 'dead', 'cancelled'].includes(status),
    run: status !== 'running',
    cancel: ['pending', 'running'].includes(status),
    delete: status !== 'running',
  }
}

/**
 * attemptOutcomeMeta describes one row of the failure log.
 *
 * Separate from taskStatusMeta because the vocabularies are different: an attempt can
 * be a timeout or lost, neither of which is a task status, and a task can be pending,
 * which is not an outcome. Collapsing them would mean one function with two unrelated
 * halves.
 */
export function attemptOutcomeMeta(outcome: string): StatusMeta {
  switch (outcome) {
    case 'succeeded':
      return { label: '成功', variant: 'success' }
    case 'failed':
      return { label: '失败', variant: 'destructive' }
    case 'timeout':
      // Distinct from a plain failure on purpose: a slow dependency and a broken one
      // call for different responses, and the error text does not distinguish them.
      return { label: '超时', variant: 'destructive' }
    case 'lost':
      // The worker vanished mid-attempt — a crash, an OOM, a killed container. Worth
      // its own label, because the handler may well be fine.
      return { label: 'worker 丢失', variant: 'destructive' }
    case 'cancelled':
      return { label: '已中断', variant: 'secondary' }
    default:
      return { label: outcome, variant: 'outline' }
  }
}

/**
 * scheduleStatusMeta describes a cron plan's state.
 *
 * The three cases are not one axis: "removed from the code" outranks paused, because a
 * plan the code no longer declares will not fire however enabled it looks, and "no
 * handler here" outranks enabled for the same reason.
 */
export function scheduleStatusMeta(plan: {
  enabled: boolean
  present: boolean
  known_kind: boolean
}): StatusMeta {
  if (!plan.present) {
    return { label: '代码中已移除', variant: 'destructive' }
  }
  if (!plan.known_kind) {
    return { label: '无处理器', variant: 'destructive' }
  }
  if (!plan.enabled) {
    return { label: '已暂停', variant: 'secondary' }
  }
  return { label: '已启用', variant: 'success' }
}
