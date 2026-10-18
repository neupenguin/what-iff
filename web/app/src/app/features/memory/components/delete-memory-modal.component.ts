import { ChangeDetectionStrategy, Component, inject, input, output } from '@angular/core';

import { AxLevelService } from '../../../core/services/ax-level.service';
import { ModalComponent } from '../../../shared/ui/modal/modal.component';

@Component({
  selector: 'app-delete-memory-modal',
  standalone: true,
  imports: [ModalComponent],
  templateUrl: './delete-memory-modal.component.html',
  styleUrl: './delete-memory-modal.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class DeleteMemoryModalComponent {
  /** Agent-experience demo level; see AxLevelService. */
  readonly ax = inject(AxLevelService);

  readonly open = input(false);
  readonly count = input(1);
  readonly deleting = input(false);

  readonly confirm = output<void>();
  readonly cancel = output<void>();
}
