import { isExtensionAllowedForChatUpload, isMimeOrExtensionAllowedForChatUpload } from './chat.constants';

describe('chat upload allowlist', () => {
  it('accepts WebP by extension, matching the API', () => {
    expect(isExtensionAllowedForChatUpload('sunset.webp')).toBe(true);
    expect(isExtensionAllowedForChatUpload('SUNSET.WEBP')).toBe(true);
  });

  it('accepts a WebP file whose browser MIME type is missing', () => {
    expect(isMimeOrExtensionAllowedForChatUpload(new File(['x'], 'sunset.webp', { type: '' }))).toBe(true);
    expect(isMimeOrExtensionAllowedForChatUpload(new File(['x'], 'sunset', { type: 'image/webp' }))).toBe(true);
  });

  it('still refuses types the API does not take', () => {
    expect(isMimeOrExtensionAllowedForChatUpload(new File(['x'], 'drawing.svg', { type: 'image/svg+xml' }))).toBe(false);
    expect(isExtensionAllowedForChatUpload('photo.heic')).toBe(false);
  });
});
