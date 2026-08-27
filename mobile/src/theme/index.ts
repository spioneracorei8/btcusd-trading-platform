/**
 * The design tokens. Import from here; never write a colour, a size or a
 * spacing value inline.
 *
 * The hex-literal lint rule (eslint.config.js) enforces the colour half
 * mechanically. The rest is convention, which is why the tokens are small
 * enough to hold in your head.
 */
import { colors } from './colors';
import { layout } from './layout';
import { type } from './type';

export * from './colors';
export * from './type';
export * from './layout';

export const theme = { colors, type, layout } as const;
