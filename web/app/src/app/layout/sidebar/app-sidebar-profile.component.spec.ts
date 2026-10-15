import { profileFullName, profileShortName } from './app-sidebar-profile.component';

describe('sidebar profile names', () => {
    const base = { username: 'jdoe', email: 'jane@example.com' };

    describe('profileShortName', () => {
        it.each([
            ['first name and last initial', { ...base, first_name: 'Jane', last_name: 'doe' }, 'Jane D.'],
            ['trims whitespace around both names', { ...base, first_name: '  Jane ', last_name: ' Doe ' }, 'Jane D.'],
            ['first name only when there is no last name', { ...base, first_name: 'Jane' }, 'Jane'],
            ['username when there is no first name', { ...base, last_name: 'Doe' }, 'jdoe'],
            ['email when there is no username', { username: '', email: 'jane@example.com' }, 'jane@example.com'],
            ['Guest when nothing is set', { username: '', email: '' }, 'Guest'],
        ])('uses the %s', (_label, user, expected) => {
            expect(profileShortName(user)).toBe(expected);
        });

        it('returns Guest when signed out', () => {
            expect(profileShortName(null)).toBe('Guest');
        });
    });

    describe('profileFullName', () => {
        it.each([
            ['first and last name', { ...base, first_name: 'Jane', last_name: 'Doe' }, 'Jane Doe'],
            ['last name alone', { ...base, last_name: 'Doe' }, 'Doe'],
            ['username when no name is set', base, 'jdoe'],
            ['Guest when nothing is set', { username: '', email: '' }, 'Guest'],
        ])('uses the %s', (_label, user, expected) => {
            expect(profileFullName(user)).toBe(expected);
        });

        it('returns Guest when signed out', () => {
            expect(profileFullName(null)).toBe('Guest');
        });
    });
});
