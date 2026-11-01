export type ImageReferenceParts = {
  image: string
  tag: string
}

export function splitImageReference(value?: string): ImageReferenceParts
