
import { ChangeDetectionStrategy, Component, computed, input, output, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';

import { Memory } from '../../../core/models/memory.model';
import { PersonalityThumbnailCircle } from '../../../core/models/personality.model';
import { GLOBAL_SCOPE_LABEL, isUserScopedMemoryLevel, levelBadgeText, levelDescription } from '../helpers/memory-vm.helpers';

export interface MemoryPersonalityOption {
  id: string;
  label: string;
  accent_color?: string | null;
  cover_image_url?: string | null;
  thumbnail_circle?: PersonalityThumbnailCircle | null;
}

@Component({
  selector: 'app-memory-form',
  standalone: true,
  imports: [FormsModule],
  templateUrl: './memory-form.component.html',
  styleUrl: './memory-form.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class MemoryFormComponent {
  readonly memory = input.required<Memory>();
  readonly personalities = input<MemoryPersonalityOption[]>([]);
  readonly saving = input(false);
  readonly deleting = input(false);
  readonly pinUpdating = input(false);

  readonly cancel = output<void>();
  readonly remove = output<void>();
  readonly save = output<{ content: string }>();
  readonly pinChange = output<string | null>();

  readonly globalScopeLabel = GLOBAL_SCOPE_LABEL;

  readonly content = signal('');
  readonly pinnedPersonalityId = signal<string | null>(null);

  readonly canSave = computed(() => this.content().trim().length > 0);
  // The level is shown, not edited here: moving across the thread boundary is the memory list's
  // Move (MemoryScopeDialogComponent), and global/personality is the pin below.
  readonly levelLabel = computed(() => levelBadgeText(this.memory().level));
  readonly levelHint = computed(() => levelDescription(this.memory().level));
  readonly showPinControl = computed(() => isUserScopedMemoryLevel(this.memory().level));

  ngOnChanges(): void {
    const memory = this.memory();
    this.content.set(memory.content);
    this.pinnedPersonalityId.set(memory.pinned_personality_id ?? null);
  }

  onPinnedPersonalityChange(next: string | null): void {
    this.pinnedPersonalityId.set(next);
    this.pinChange.emit(next);
  }

  onSubmit(): void {
    if (!this.canSave()) return;
    this.save.emit({ content: this.content().trim() });
  }
}
