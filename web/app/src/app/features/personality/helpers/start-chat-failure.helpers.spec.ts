import { startChatFailureAlert } from './start-chat-failure.helpers';

describe('startChatFailureAlert', () => {
  it('names the personality and carries the server reason', () => {
    const alert = startChatFailureAlert('Imported Card', new Error('Personality not found'));

    expect(alert.title).toBe('Could not start a chat with Imported Card');
    expect(alert.message).toContain('Personality not found');
    expect(alert.message).toContain('The personality list has been refreshed');
  });

  it('falls back to generic copy for a blank name and an unexplained failure', () => {
    const alert = startChatFailureAlert('  ', {});

    expect(alert.title).toBe('Could not start a chat with this personality');
    expect(alert.message.startsWith('Something went wrong.')).toBe(true);
  });
});
