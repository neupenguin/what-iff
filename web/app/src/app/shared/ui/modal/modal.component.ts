import { DOCUMENT } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  ElementRef,
  OnDestroy,
  computed,
  effect,
  inject,
  input,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { AxLevelService } from '../../../core/services/ax-level.service';
import { lockBodyScroll, releaseBodyScroll, type BodyScrollLockHandle } from '../helpers/body-scroll-lock.helpers';
import { createFocusTrap, releaseFocusTrap, type FocusTrapHandle } from '../helpers/focus-trap.helpers';
import { isEscapeKey } from '../helpers/keyboard.helpers';
import { XIconComponent } from '../icons';

export type ModalSize = 'sm' | 'md' | 'lg' | 'xl';

/**
 * How a dismiss was triggered. Consumers can use this to guard "soft" dismissals
 * (backdrop click / Escape) behind an unsaved-changes confirmation while letting
 * the explicit header close button dismiss immediately.
 */
export type ModalDismissReason = 'backdrop' | 'escape' | 'close-button';

const SIZE_CLASSES: Record<ModalSize, string> = {
  sm: 'max-w-sm',
  md: 'max-w-md',
  lg: 'max-w-2xl',
  xl: 'max-w-4xl',
};

@Component({
  selector: 'ui-modal',
  standalone: true,
  imports: [XIconComponent],
  templateUrl: './modal.component.html',
  styleUrl: './modal.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ModalComponent implements OnDestroy {
  readonly open = input(false);
  readonly labelledBy = input<string | null>(null);
  readonly describedBy = input<string | null>(null);
  readonly dismissible = input(true);
  readonly size = input<ModalSize>('md');
  /**
   * Opt in to the agent-experience demo's degradation levels (AxLevelService). Only features/memory
   * sets this, so every other modal in the app keeps its semantics at any `?ax` level.
   */
  readonly axDegrade = input(false);
  readonly dismiss = output<ModalDismissReason>();

  readonly dialog = viewChild<ElementRef<HTMLElement>>('dialog');

  private readonly document = inject(DOCUMENT);
  private readonly ax = inject(AxLevelService);

  /** The ax level this modal honours: the app-wide level when opted in, otherwise 0. */
  // Level 4 (patterns) keeps full dialog semantics, so the modal treats it like level 0.
  readonly axLevel = computed(() => (this.axDegrade() && this.ax.level() !== 4 ? this.ax.level() : 0));
  /** Accessible name for the close button (dropped from ax level 1). */
  readonly closeLabel = computed(() => (this.axLevel() >= 1 ? null : 'Close'));
  /** Dialog role and its aria-modal/labelling (dropped from ax level 2). */
  readonly dialogRole = computed(() => (this.axLevel() >= 2 ? null : 'dialog'));
  readonly ariaModal = computed(() => (this.axLevel() >= 2 ? null : 'true'));
  readonly ariaLabelledBy = computed(() => (this.axLevel() >= 2 ? null : this.labelledBy()));
  readonly ariaDescribedBy = computed(() => (this.axLevel() >= 2 ? null : this.describedBy()));
  private focusTrap: FocusTrapHandle | null = null;
  private bodyLock: BodyScrollLockHandle | null = null;

  constructor() {
    effect(() => {
      if (this.open()) {
        this.lockBody();
        setTimeout(() => this.activateFocusTrap(), 0);
      } else {
        this.releaseFocusTrap();
        this.releaseBody();
      }
    });
  }

  ngOnDestroy(): void {
    this.releaseFocusTrap();
    this.releaseBody();
  }

  panelClass(): string {
    return `ui-modal__panel w-full ${SIZE_CLASSES[this.size()]}`;
  }

  backdropClass(): string {
    return 'ui-modal fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4';
  }

  backdropRole(): string | null {
    return this.axLevel() >= 2 ? null : 'presentation';
  }

  onBackdropClick(): void {
    this.requestDismiss('backdrop');
  }

  onDialogClick(event: Event): void {
    event.stopPropagation();
  }

  onKeydown(event: KeyboardEvent): void {
    if (isEscapeKey(event) && this.dismissible()) {
      event.preventDefault();
      this.dismiss.emit('escape');
    }
  }

  requestDismiss(reason: ModalDismissReason = 'close-button'): void {
    if (this.dismissible()) {
      this.dismiss.emit(reason);
    }
  }

  private lockBody(): void {
    if (!this.bodyLock) {
      this.bodyLock = lockBodyScroll(this.document.body);
    }
  }

  private releaseBody(): void {
    releaseBodyScroll(this.bodyLock);
    this.bodyLock = null;
  }

  private activateFocusTrap(): void {
    if (!this.open() || this.focusTrap) {
      return;
    }

    const dialog = this.dialog()?.nativeElement;
    if (dialog) {
      this.focusTrap = createFocusTrap(dialog);
    }
  }

  private releaseFocusTrap(): void {
    releaseFocusTrap(this.focusTrap);
    this.focusTrap = null;
  }
}
