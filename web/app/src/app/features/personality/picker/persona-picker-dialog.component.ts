import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  effect,
  inject,
  input,
  output,
  signal,
  untracked,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Subscription } from 'rxjs';

import { Personality } from '../../../core/models/personality.model';
import { PersonalityService } from '../../../core/services/personality.service';
import { ModalComponent } from '../../../shared/ui/modal/modal.component';
import { PersonaPickerComponent } from './persona-picker.component';

/**
 * Dialog wrapper around `persona-picker`. Fetches the personality list every
 * time the dialog opens and re-emits the picker's `select` / `cancel` /
 * `create` outputs to the parent.
 *
 * The app layout keeps one instance mounted for the whole session, so the
 * list must not be loaded once and kept: a personality created or imported
 * after the first open would be missing (or a deleted one still offered)
 * until a page reload (#318). Reopening shows the last list straight away and
 * swaps in the fresh one when it arrives; only the first open shows the
 * loading state. A change published while the dialog is open refetches too.
 */
@Component({
  selector: 'app-persona-picker-dialog',
  standalone: true,
  imports: [ModalComponent, PersonaPickerComponent],
  template: `
    <ui-modal [open]="open()" [labelledBy]="labelId" size="lg" (dismiss)="onCancel()">
      <div modal-header>
        <div class="grid gap-1">
          <h2 [id]="labelId" class="text-base font-semibold text-(--color-text-primary)">{{ title() }}</h2>
          @if (subtitle()) {
            <p class="m-0 text-xs text-(--color-text-secondary)">{{ subtitle() }}</p>
          }
        </div>
      </div>
      @if (loading()) {
        <p class="text-sm text-(--color-text-secondary)" role="status">Loading personalities…</p>
      } @else if (errorMessage()) {
        <p class="rounded-md border border-red-500 bg-red-500/10 px-3 py-2 text-sm text-red-700 dark:text-red-300" role="alert">{{ errorMessage() }}</p>
      } @else {
        <persona-picker
          [personalities]="personalities()"
          [ariaLabel]="title()"
          [appearance]="pickerAppearance()"
          (select)="onSelect($event)"
          (cancel)="onCancel()"
          (create)="onCreate()"
        />
      }
    </ui-modal>
  `,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class PersonaPickerDialogComponent {
  readonly open = input<boolean>(false);
  readonly title = input<string>('Pick a personality');
  readonly subtitle = input<string | null>(null);
  readonly pickerAppearance = input<'rich' | 'filter-list'>('rich');

  readonly select = output<Personality>();
  readonly cancel = output<void>();
  readonly create = output<void>();

  private readonly personalityService = inject(PersonalityService);
  private readonly destroyRef = inject(DestroyRef);

  readonly labelId = `persona-picker-dialog-${randomId()}`;
  readonly personalities = signal<Personality[]>([]);
  readonly loading = signal(false);
  readonly errorMessage = signal<string | null>(null);

  private hasLoaded = false;
  private inFlight: Subscription | null = null;

  constructor() {
    // Each closed -> open transition refetches; staying open does not.
    effect(() => {
      if (this.open()) untracked(() => this.load());
    });

    this.personalityService.changes$
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        if (this.open()) this.load();
      });

    this.destroyRef.onDestroy(() => this.inFlight?.unsubscribe());
  }

  refresh(): void {
    this.load();
  }

  onSelect(personality: Personality): void {
    this.select.emit(personality);
  }

  onCancel(): void {
    this.cancel.emit();
  }

  onCreate(): void {
    this.create.emit();
  }

  private load(): void {
    this.inFlight?.unsubscribe();
    // Stale-while-revalidate: once a list has been shown, keep it on screen while refetching.
    if (!this.hasLoaded) this.loading.set(true);
    this.errorMessage.set(null);
    this.inFlight = this.personalityService.listPersonalities(1, 100).subscribe({
      next: response => {
        this.personalities.set(response.results ?? []);
        this.loading.set(false);
        this.hasLoaded = true;
      },
      error: err => {
        console.error('Failed to load personalities', err);
        this.loading.set(false);
        // A failed background refresh keeps the list the user can already see.
        if (!this.hasLoaded) this.errorMessage.set('Failed to load personalities. Please try again.');
      },
    });
  }
}

function randomId(): string {
  return Math.random().toString(36).slice(2, 10);
}
