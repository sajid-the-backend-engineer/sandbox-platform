/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { ImgHTMLAttributes } from 'react'
import logoBlack from './northrays-logo-black.png'
import logoWhite from './northrays-logo-white.png'
import wordmarkBlack from './northrays-full-black.png'
import wordmarkWhite from './northrays-full-white.png'

type LogoProps = Omit<ImgHTMLAttributes<HTMLImageElement>, 'src' | 'alt'>

// Light and dark variants switched with the shell's `dark:` classes; a raster
// mark cannot take currentColor the way the previous inline SVG did.
function ThemedImage({
  light,
  dark,
  className,
  ...props
}: LogoProps & { light: string; dark: string }) {
  const base = className ?? ''
  return (
    <>
      <img src={light} alt="Northrays" className={`${base} dark:hidden`} draggable={false} {...props} />
      <img src={dark} alt="Northrays" className={`${base} hidden dark:block`} draggable={false} {...props} />
    </>
  )
}

export function Logo(props: LogoProps) {
  return <ThemedImage light={logoBlack} dark={logoWhite} className={props.className ?? 'h-7 w-auto'} {...props} />
}

export function LogoText(props: LogoProps) {
  return (
    <ThemedImage light={wordmarkBlack} dark={wordmarkWhite} className={props.className ?? 'h-7 w-auto'} {...props} />
  )
}
