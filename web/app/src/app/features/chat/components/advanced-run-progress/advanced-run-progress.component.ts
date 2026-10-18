import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';

import { ChatTurnAdvancedProgress, ChatTurnPlanStatus } from '../../../../core/models/job.model';

/** Screen-reader wording and the visible marker for each plan item status. */
const PLAN_STATUS: Record<ChatTurnPlanStatus, { label: string; marker: string }> = {
  pending: { label: 'To do', marker: '○' },
  in_progress: { label: 'In progress', marker: '◐' },
  completed: { label: 'Done', marker: '●' },
  cancelled: { label: 'Dropped', marker: '⊘' },
};

/**
 * An in-flight Advanced turn's step and plan checklist (ADR 0x026), shown with the live tool
 * timeline. Display-only; the plan is whatever the model last wrote with `update_plan`.
 */
@Component({
  selector: 'app-advanced-run-progress',
  standalone: true,
  template: `
    <div class="advanced-run">
      <p class="advanced-run__step" role="status">
        Step {{ progress().step }}
        @if (summary(); as text) {
          <span class="advanced-run__summary"> · {{ text }}</span>
        }
      </p>
      @if (progress().plan.length > 0) {
        <ul class="advanced-run__plan" aria-label="Plan">
          @for (item of progress().plan; track $index) {
            <li class="advanced-run__item" [class]="'advanced-run__item--' + item.status">
              <span class="advanced-run__marker" aria-hidden="true">{{ statusMarker(item.status) }}</span>
              <span class="sr-only">{{ statusLabel(item.status) + ': ' }}</span>
              <span class="advanced-run__content">{{ item.content }}</span>
            </li>
          }
        </ul>
      }
    </div>
  `,
  styles: [
    `
      .advanced-run {
        background: color-mix(in srgb, var(--color-surface-muted) 64%, var(--color-surface-base));
        border: 1px solid var(--color-border-base);
        border-radius: 0.5rem;
        color: var(--color-text-secondary);
        font-size: 0.75rem;
        margin-bottom: 0.375rem;
        padding: 0.4375rem 0.75rem;
        width: min(100%, 35rem);
      }

      .advanced-run__step {
        color: var(--color-text-muted);
        font-size: 0.6875rem;
        font-weight: 600;
        letter-spacing: 0.02em;
        margin: 0;
      }

      .advanced-run__summary {
        font-weight: 400;
      }

      .advanced-run__plan {
        display: grid;
        gap: 0.125rem;
        list-style: none;
        margin: 0.375rem 0 0;
        padding: 0;
      }

      .advanced-run__item {
        align-items: baseline;
        display: flex;
        gap: 0.5rem;
      }

      .advanced-run__marker {
        flex-shrink: 0;
        width: 0.75rem;
      }

      .advanced-run__item--in_progress {
        color: var(--color-text-primary);
        font-weight: 600;
      }

      .advanced-run__item--in_progress .advanced-run__marker {
        color: var(--color-accent);
      }

      .advanced-run__item--completed .advanced-run__content,
      .advanced-run__item--cancelled .advanced-run__content {
        color: var(--color-text-muted);
      }

      .advanced-run__item--cancelled .advanced-run__content {
        text-decoration: line-through;
      }
    `,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AdvancedRunProgressComponent {
  readonly progress = input.required<ChatTurnAdvancedProgress>();

  /** "2 of 5 done" once there is a plan; empty otherwise. */
  readonly summary = computed(() => {
    const plan = this.progress().plan.filter(item => item.status !== 'cancelled');
    if (plan.length === 0) return '';
    const done = plan.filter(item => item.status === 'completed').length;
    return `${done} of ${plan.length} done`;
  });

  statusLabel(status: ChatTurnPlanStatus): string {
    return PLAN_STATUS[status].label;
  }

  statusMarker(status: ChatTurnPlanStatus): string {
    return PLAN_STATUS[status].marker;
  }
}
