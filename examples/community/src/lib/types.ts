export interface PublicProject {
  id: string
  name: string
  domain_prefix: string
  url: string
  auth_required: boolean
  owner_name: string
  owner_avatar_url: string
  updated_at: number // epoch seconds
}
