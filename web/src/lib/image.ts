/** The server's cap on an application icon's data: URL. */
export const MAX_ICON_BYTES = 64 * 1024;

const TYPES = /^image\/(png|jpeg|webp|gif)$/;

/**
 * Turns an uploaded image into a square icon: centre-cropped, scaled to
 * `size` px and encoded as a data: URL (WebP where the browser can, else
 * PNG, else JPEG if that's what fits).
 */
export async function iconFromFile(file: File, size = 128): Promise<string> {
	if (!TYPES.test(file.type)) throw new Error('Choose a PNG, JPEG, WebP or GIF image.');
	if (file.size > 10 * 1024 * 1024) throw new Error('That image is larger than 10 MB.');
	const bmp = await createImageBitmap(file);
	const side = Math.min(bmp.width, bmp.height);
	const canvas = document.createElement('canvas');
	canvas.width = canvas.height = size;
	const ctx = canvas.getContext('2d');
	if (!ctx) throw new Error("This browser can't resize images.");
	ctx.imageSmoothingQuality = 'high';
	ctx.drawImage(bmp, (bmp.width - side) / 2, (bmp.height - side) / 2, side, side, 0, 0, size, size);
	bmp.close();

	let url = canvas.toDataURL('image/webp', 0.9);
	if (!url.startsWith('data:image/webp')) url = canvas.toDataURL('image/png');
	if (url.length > MAX_ICON_BYTES) url = canvas.toDataURL('image/jpeg', 0.85);
	if (url.length > MAX_ICON_BYTES) throw new Error("That image is too detailed to use as an icon.");
	return url;
}
