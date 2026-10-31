export interface BrandIcon {
  title: string
  hex: string
  path: string
}

export function iconForImage(image?: string): BrandIcon | undefined
export function iconForProject(project: { containers: Array<{ image?: string }> }): BrandIcon | undefined
