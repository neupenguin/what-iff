import { apiErrorMessage } from '../../../core/utils/api-error.helpers';

/**
 * Alert copy for a personality picked in the persona picker whose new chat could not be created.
 * The usual cause is a picker entry for a personality that no longer exists (deleted, or replaced
 * by a re-import), which the server answers with "Personality not found"; callers refresh the
 * cached personality lists alongside showing this, so picking again offers the current records.
 */
export function startChatFailureAlert(personalityName: string, error: unknown): { title: string; message: string } {
  const name = personalityName.trim() || 'this personality';
  return {
    title: `Could not start a chat with ${name}`,
    message: `${apiErrorMessage(error, 'Something went wrong.')}\n\nThe personality list has been refreshed. Pick the personality again to retry.`,
  };
}
