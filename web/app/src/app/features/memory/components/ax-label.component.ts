import { NgTemplateOutlet } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject } from '@angular/core';

import { AxLevelService } from '../../../core/services/ax-level.service';

/**
 * A `<label>` that stops being one at agent-experience level 1+ (AxLevelService).
 *
 * Wrapping a control in `<label>` is what gives it its accessible name here, so the level-1
 * degradation renders a `<div>` instead: the visible text stays, the association goes. The inner
 * element is `display: contents`, so the host box (which carries the caller's class and styles)
 * lays out the children exactly as the original `<label class="…">` did.
 */
@Component({
  selector: 'app-ax-label',
  standalone: true,
  imports: [NgTemplateOutlet],
  template: `
    <ng-template #content><ng-content /></ng-template>
    @if (ax.names()) {
      <label class="ax-label"><ng-container [ngTemplateOutlet]="content" /></label>
    } @else {
      <div class="ax-label"><ng-container [ngTemplateOutlet]="content" /></div>
    }
  `,
  styles: `
    .ax-label {
      display: contents;
    }
  `,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AxLabelComponent {
  readonly ax = inject(AxLevelService);
}
