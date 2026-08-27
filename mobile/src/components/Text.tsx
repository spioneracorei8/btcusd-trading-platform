import { Text as RNText, TextProps, TextStyle } from 'react-native';

import { colors, type } from '../theme';

type Tone = 'primary' | 'secondary' | 'tertiary';
type Size = keyof typeof type.size;

export type AppTextProps = TextProps & {
  tone?: Tone;
  size?: Size;
  weight?: keyof typeof type.weight;
  /** Numbers get tabular figures. See `Num` — you almost always want that. */
  tabular?: boolean;
};

const TONES: Record<Tone, string> = {
  primary: colors.text.primary,
  secondary: colors.text.secondary,
  tertiary: colors.text.tertiary,
};

/** Text on the scale, in a tone from the palette. */
export function Text({
  tone = 'primary',
  size = 'body',
  weight = 'regular',
  tabular = false,
  style,
  ...rest
}: AppTextProps) {
  const base: TextStyle = {
    color: TONES[tone],
    fontSize: type.size[size],
    fontWeight: type.weight[weight],
    ...(tabular ? type.tabular : null),
  };
  return <RNText {...rest} style={[base, style]} />;
}

/**
 * A number.
 *
 * Always tabular. Proportional digits change width as they change value, so a
 * live price jitters horizontally while it updates and a column of returns
 * fails to line up. There is no way to opt out, which is the point.
 */
export function Num({ style, ...rest }: Omit<AppTextProps, 'tabular'>) {
  return <Text {...rest} tabular style={style} />;
}
