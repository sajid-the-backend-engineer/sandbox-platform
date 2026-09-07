/*
 * Copyright 2025 Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { motion, MotionProps } from 'motion/react'
import { ComponentProps } from 'react'
import wordmarkBlack from '@/assets/northrays-full-black.png'
import wordmarkWhite from '@/assets/northrays-full-white.png'

// The previous implementation animated the letters of an inline SVG wordmark.
// The Northrays wordmark ships as raster assets, so the reveal is a single
// fade-and-settle on the image instead. Two variants rather than
// currentColor: a PNG cannot inherit the text colour, so light and dark are
// separate files switched by the same `dark:` classes the rest of the shell
// uses, which keeps this component free of any theme-hook dependency.
const revealProps: MotionProps = {
  initial: { filter: 'blur(2px)', x: -4, opacity: 0 },
  animate: { filter: 'blur(0px)', x: 0, opacity: 1 },
  transition: { duration: 0.25, ease: 'easeInOut' },
}

type AnimatedLogoProps = Omit<ComponentProps<typeof motion.div>, 'children'> & {
  animated?: boolean
}

export function AnimatedLogo({ animated = true, className, ...props }: AnimatedLogoProps) {
  return (
    <motion.div className={className} {...(animated ? revealProps : {})} {...props}>
      <img src={wordmarkBlack} alt="Northrays" className="block h-auto w-full dark:hidden" draggable={false} />
      <img src={wordmarkWhite} alt="Northrays" className="hidden h-auto w-full dark:block" draggable={false} />
    </motion.div>
  )
}
