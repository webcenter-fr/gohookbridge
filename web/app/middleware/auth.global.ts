export default defineNuxtRouteMiddleware(async (to) => {
  const auth = useAuthStore()
  await auth.checkSession()

  if (to.meta.public !== true && !auth.isAuthenticated) {
    return navigateTo({ path: '/login', query: { redirect: to.fullPath } })
  }
  if (to.meta.guest === true && auth.isAuthenticated) {
    return navigateTo('/')
  }
})
