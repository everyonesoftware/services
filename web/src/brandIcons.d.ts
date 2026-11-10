export interface BrandIcon {
  title: string
  slug: string
  src: string
}

export function iconForImage(image?: string): BrandIcon | undefined
export function iconForProject(project: { name?: string; containers: Array<{ image?: string }> }): BrandIcon | undefined
