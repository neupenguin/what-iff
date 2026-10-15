import { ChangeDetectionStrategy, Component, computed, inject, input } from '@angular/core';

import { UserResponse } from '../../core/models/user.model';
import { AuthService } from '../../core/services/auth.service';
import { ProfileSettingsModalService } from '../../features/profile-settings/profile-settings-modal.service';
import { UserIconComponent } from '../../shared/ui/icons/icons';
import { TooltipDirective } from '../../shared/ui/tooltip/tooltip.directive';

type ProfileUser = Pick<UserResponse, 'username' | 'email' | 'first_name' | 'last_name'>;

/** Full name for accessible labels: "First Last", falling back to username, email, then "Guest". */
export function profileFullName(user: ProfileUser | null): string {
  if (!user) return 'Guest';
  const composed = [user.first_name, user.last_name].map(part => part?.trim()).filter(Boolean).join(' ');
  return composed || user.username || user.email || 'Guest';
}

/** Compact name for the footer: "First L.", then first name only, username, email, "Guest". */
export function profileShortName(user: ProfileUser | null): string {
  if (!user) return 'Guest';
  const first = user.first_name?.trim();
  const lastInitial = user.last_name?.trim().charAt(0).toUpperCase();
  if (first && lastInitial) return `${first} ${lastInitial}.`;
  return first || user.username || user.email || 'Guest';
}

/**
 * Sidebar footer profile trigger: user icon plus a compact name when expanded,
 * icon only (with tooltip) on the collapsed rail. Opens the profile modal.
 */
@Component({
  selector: 'app-sidebar-profile',
  standalone: true,
  imports: [TooltipDirective, UserIconComponent],
  templateUrl: './app-sidebar-profile.component.html',
  styleUrl: './app-sidebar-profile.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AppSidebarProfileComponent {
  readonly collapsed = input(false);

  private readonly auth = inject(AuthService);
  private readonly profileSettingsModal = inject(ProfileSettingsModalService);

  readonly fullName = computed(() => profileFullName(this.auth.currentUser()));
  readonly shortName = computed(() => profileShortName(this.auth.currentUser()));

  openProfile(): void {
    this.profileSettingsModal.open('profile');
  }
}
