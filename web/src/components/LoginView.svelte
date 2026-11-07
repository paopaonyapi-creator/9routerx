<!-- 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id -->
<script lang="ts">
  import { onMount, tick } from 'svelte'
  import { api } from '../api/client'
  import AsciiRoutingBg from './AsciiRoutingBg.svelte'
  import { routingBurst } from '../lib/routingBurst'
  import { scramble } from '../lib/scramble'

  let {
    onSuccess,
    // autoResume=false blocks the onMount session-resume: right after an
    // explicit logout the auth/status probe still answers requireLogin=false
    // on an open-dashboard install, and resuming then would bounce the
    // operator straight back into the dashboard they just left.
    autoResume = true,
  }: {
    onSuccess?: () => void
    autoResume?: boolean
  } = $props()

  let password = $state('')
  let newPassword = $state('')
  let showPassword = $state(false)
  let isLoading = $state(false)
  let errorMessage = $state('')
  let resetHint = $state('')
  let retryAfter = $state(0)
  let hasPassword = $state<boolean | null>(null)
  let authMode = $state('password')
  let ssoType = $state('oidc')
  let oidcConfigured = $state(false)
  let oidcLoginLabel = $state('Sign in with OIDC')
  let samlConfigured = $state(false)
  let samlLoginLabel = $state('Sign in with SAML SSO')
  let mustChange = $state(false)

  // Countdown for rate-limit lockout (upstream retryAfter).
  $effect(() => {
    if (retryAfter <= 0) return
    const id = setInterval(() => {
      retryAfter = retryAfter > 0 ? retryAfter - 1 : 0
    }, 1000)
    return () => clearInterval(id)
  })

  onMount(() => {
    // Surface SSO callback failures (?error=...) like upstream /login.
    const queryError = new URLSearchParams(window.location.search).get('error')
    if (queryError) errorMessage = queryError
    const controller = new AbortController()
    const timeoutId = setTimeout(() => controller.abort(), 5000)
    fetch('/api/auth/status', { signal: controller.signal })
      .then(async (res) => {
        clearTimeout(timeoutId)

        if (!res.ok) {
          // Safe fallback to avoid an infinite loading state.
          hasPassword = true
          tick().then(focusPasswordField)
          return
        }
        const data = await res.json()
        if (autoResume && (data.authenticated === true || data.requireLogin === false)) {
          if (onSuccess) {
            onSuccess()
          } else {
            window.location.assign('/dashboard')
          }
          return
        }
        hasPassword = !!data.hasPassword
        authMode = data.authMode || 'password'
        ssoType = data.ssoType || 'oidc'
        oidcConfigured = data.oidcConfigured === true
        oidcLoginLabel = data.oidcLoginLabel || 'Sign in with OIDC'
        samlConfigured = data.samlConfigured === true
        samlLoginLabel = data.samlLoginLabel || 'Sign in with SAML SSO'
        // The form only exists once hasPassword has left null, so focus it as
        // soon as the state that reveals it has been applied.
        tick().then(focusPasswordField)
      })
      .catch(() => {
        clearTimeout(timeoutId)
        hasPassword = true
        tick().then(focusPasswordField)
      })
  })

  // Focus the field the user has to type into, once the form is on screen.
  //
  // The `autofocus` attribute was the obvious way to do this and it warned in
  // the console: "Autofocus processing was blocked because a document already
  // has a focused element." Reached from /dashboard without a session the app
  // mounts this view beside the page it was already showing, and the browser
  // resolves autofocus per document — the body had already taken focus, so the
  // attribute was refused. Asking for focus after mount gets the same result
  // without asking the browser to arbitrate.
  //
  // Desktop only: on a phone the auto-raised keyboard would cover the panel,
  // so the field waits for a tap there (glyph spec 5.3).
  function focusPasswordField() {
    if (!window.matchMedia('(min-width: 768px)').matches) return
    document.querySelector<HTMLElement>('#login-password')?.focus()
  }

  type LoginFailure = Error & {
    retryAfter?: number
    resetHint?: string
    mustChangePassword?: boolean
  }

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault()
    if (!password.trim() || isLoading || retryAfter > 0) return

    isLoading = true
    errorMessage = ''
    resetHint = ''

    try {
      const res = await api.login(password)
      if (res.success) {
        // Live routing flow (TASK-9routerX-anim-bg 2.5.2), wiring point 2 of
        // 2: a successful login submit fires a background data burst.
        routingBurst()
        if (onSuccess) {
          onSuccess()
        } else {
          window.location.assign('/dashboard')
        }
      } else {
        errorMessage = res.error || 'Invalid password'
      }
    } catch (err: unknown) {
      const failure = err as LoginFailure
      if (failure?.mustChangePassword) {
        // Remote fresh install on the well-known default: rotate first. The
        // login attempt never issues a session, so set the new password from
        // the local host (or set INITIAL_PASSWORD) before retrying.
        mustChange = true
        errorMessage = err instanceof Error ? err.message : 'Default password must be changed before remote access.'
      } else {
        errorMessage = err instanceof Error ? err.message : 'Invalid password'
        if (typeof failure?.retryAfter === 'number') retryAfter = failure.retryAfter
        if (typeof failure?.resetHint === 'string') resetHint = failure.resetHint
      }
    } finally {
      isLoading = false
    }
  }

  // Force a new password before entering the dashboard (default + remote).
  async function handleSetNewPassword(e: SubmitEvent) {
    e.preventDefault()
    if (!newPassword || isLoading) return

    isLoading = true
    errorMessage = ''

    try {
      const res = await api.login(password, newPassword)
      if (res.success) {
        if (onSuccess) {
          onSuccess()
        } else {
          window.location.assign('/dashboard')
        }
      } else {
        errorMessage = res.error || 'Failed to set password'
      }
    } catch (err: unknown) {
      errorMessage = err instanceof Error ? err.message : 'Failed to set password'
    } finally {
      isLoading = false
    }
  }

  function handleOidcLogin() {
    window.location.href = '/api/auth/oidc/start'
  }

  function handleSamlLogin() {
    window.location.href = '/api/auth/saml/start'
  }

  const isSsoEnabled = $derived(['sso', 'oidc', 'saml', 'both'].includes(authMode))
  const activeSsoType = $derived(ssoType || (authMode === 'saml' ? 'saml' : 'oidc'))
  const samlAvailable = $derived(isSsoEnabled && activeSsoType === 'saml' && samlConfigured)
  const oidcAvailable = $derived(isSsoEnabled && activeSsoType === 'oidc' && oidcConfigured)
  const ssoAvailable = $derived(samlAvailable || oidcAvailable)
  const passwordAvailable = $derived(authMode === 'password' || authMode === 'both' || !ssoAvailable)

  const version = typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : ''
  const GITHUB_URL = 'https://github.com/yanstd888/9routerx'
  const DOCS_URL = 'https://github.com/yanstd888/9routerx/wiki'
</script>

<div class="relative min-h-screen flex flex-col overflow-hidden bg-[var(--9x-bg)] text-[var(--9x-ink)]">
  <!--
    ASCII routing-map background mount point (FASE UI-GLEPH, later commit).
    The background component slots into this layer — it sits behind the
    gridline overlay and the glass panel, and must render into it (canvas or
    one <pre>) so the panel's backdrop-blur blurs the tiles behind it.
  -->
  <div id="glyph-bg-mount" data-glyph-bg="mount" class="absolute inset-0" aria-hidden="true">
    <AsciiRoutingBg />
  </div>
  <div
    class="absolute inset-0 pointer-events-none"
    aria-hidden="true"
    style="background-image: linear-gradient(to right, var(--9x-gridline) 1px, transparent 1px), linear-gradient(to bottom, var(--9x-gridline) 1px, transparent 1px); background-size: var(--9x-cell) var(--9x-cell);"
  ></div>

  <!-- Thin top header -->
  <header class="relative z-10 flex items-center justify-between px-4 sm:px-6 h-14 shrink-0">
    <div class="flex items-center gap-2.5 min-w-0">
      <img src="/android-chrome-192x192.png" alt="" class="size-6 object-contain" />
      <span class="font-headline font-bold tracking-tight text-[15px]">9RouterX</span>
      {#if version}
        <span class="hidden sm:inline font-code text-[11px] text-[var(--9x-ink)]/40">v{version}</span>
      {/if}
    </div>
    <!-- Pills live top-right on every viewport (owner request: on mobile they
         belong in the header corner, not below the panel) -->
    <nav class="flex items-center gap-2" aria-label="Resources">
      <a href={DOCS_URL} target="_blank" rel="noopener noreferrer" class="gl-pill">docs</a>
      <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer" class="gl-pill">github</a>
    </nav>
  </header>

  <!-- Center column -->
  <main class="relative z-10 flex-1 flex items-center justify-center p-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
    <div class="flex items-start justify-center gap-10 w-full">
      <div class="w-full max-w-[400px]">
      {#if hasPassword === null}
        <div class="gl-panel flex items-center justify-center py-10">
          <span class="gl-dots" role="status" aria-label="Loading"><i></i><i></i><i></i></span>
        </div>
      {:else}
          <div class="gl-panel" class:gl-shake={errorMessage}>
            <!-- Lockup -->
            <div class="flex items-center gap-2.5 mb-5">
              <img src="/android-chrome-192x192.png" alt="9RouterX" class="size-7 object-contain" />
              <span class="font-code text-[10px] tracking-[0.18em] text-[var(--9x-ink)]/40">9R Next Go Generation</span>
            </div>

            <!-- Text-scramble decode on first appearance (spec 2.5.4); the
                 action only rewrites text node values, so the accent span
                 below survives it. -->
            <h1 class="font-headline font-light lowercase text-[clamp(24px,5vw,30px)] leading-tight mb-6 tracking-tight" use:scramble>
              welcome back, <span class="text-[var(--9x-accent)]">&rarr;9x:</span>
            </h1>

            {#if mustChange}
              <form onsubmit={handleSetNewPassword} class="flex flex-col gap-4">
                <p class="text-sm text-[var(--9x-wait)] text-center font-code text-xs leading-relaxed">
                  Set a new password before accessing the dashboard remotely.
                </p>
                <div class="flex flex-col gap-2">
                  <label for="login-new-password" class="text-sm font-medium text-[var(--9x-ink)]/80 font-headline">New password</label>
                  <input
                    id="login-new-password"
                    type="password"
                    placeholder="Enter new password"
                    bind:value={newPassword}
                    required
                    data-login-focus
                    class="gl-input"
                  />
                  {#if errorMessage}
                    <p class="gl-error">{errorMessage}</p>
                  {/if}
                </div>
                <button type="submit" disabled={isLoading || !newPassword} class="gl-btn">
                  {#if isLoading}
                    <span class="gl-dots" aria-hidden="true"><i></i><i></i><i></i></span>
                    <span>routing...</span>
                  {:else}
                    <span>set password</span>
                  {/if}
                </button>
                <p class="gl-kbd-hint hidden md:block" aria-hidden="true">&#8629; set password</p>
              </form>
            {:else}
              <div class="flex flex-col gap-4">
                {#if samlAvailable}
                  <button type="button" onclick={handleSamlLogin} class="gl-btn">{samlLoginLabel}</button>
                {/if}

                {#if oidcAvailable}
                  <button type="button" onclick={handleOidcLogin} class="gl-btn">{oidcLoginLabel}</button>
                {/if}

                {#if ssoAvailable && passwordAvailable}
                  <div class="h-px bg-[var(--9x-line)]"></div>
                {/if}

                {#if passwordAvailable}
                  <form onsubmit={handleSubmit} class="flex flex-col gap-4">
                    {#if isSsoEnabled && !ssoAvailable}
                      <p class="text-xs text-[var(--9x-wait)] text-center font-code leading-relaxed">
                        {activeSsoType === 'saml' ? 'SAML SSO' : 'OIDC'} login is enabled, but configuration is incomplete. Password login is still available for recovery.
                      </p>
                    {/if}

                    {#if authMode === 'both' && ssoAvailable}
                      <p class="text-xs text-[var(--9x-ink)]/50 text-center font-code">
                        Password and {activeSsoType === 'saml' ? 'SAML SSO' : 'OIDC'} login are both enabled.
                      </p>
                    {/if}

                    <div class="flex flex-col gap-2">
                      <label for="login-password" class="text-sm font-medium text-[var(--9x-ink)]/80 font-headline">Password</label>
                      <div class="relative">
                        <input
                          id="login-password"
                          type={showPassword ? 'text' : 'password'}
                          placeholder="Enter password"
                          bind:value={password}
                          required
                          data-login-focus
                          class="gl-input gl-input-trailing"
                        />
                        <button
                          type="button"
                          onclick={() => (showPassword = !showPassword)}
                          class="absolute inset-y-0 right-0 flex items-center justify-center w-11 text-[var(--9x-ink)]/50 hover:text-[var(--9x-ink)] transition-colors cursor-pointer"
                          aria-label={showPassword ? 'Hide password' : 'Show password'}
                        >
                          <span class="material-symbols-outlined text-[20px]">
                            {showPassword ? 'visibility_off' : 'visibility'}
                          </span>
                        </button>
                      </div>
                      {#if errorMessage}
                        <p class="gl-error" role="alert">{errorMessage}</p>
                      {/if}
                      {#if retryAfter > 0}
                        <p class="text-xs text-[var(--9x-wait)] font-code">
                          Locked. Retry in <span>{retryAfter}s</span>.
                        </p>
                      {/if}
                      {#if resetHint}
                        <p class="text-xs text-[var(--9x-ink)]/50 font-code">{resetHint}</p>
                      {/if}
                    </div>

                    <button
                      type="submit"
                      disabled={isLoading || !password || retryAfter > 0}
                      class="gl-btn"
                    >
                      {#if isLoading}
                        <span class="gl-dots" aria-hidden="true"><i></i><i></i><i></i></span>
                        <span>routing...</span>
                      {:else if retryAfter > 0}
                        <span>wait {retryAfter}s</span>
                      {:else}
                        <span>sign in</span>
                      {/if}
                    </button>

                    <p class="gl-kbd-hint hidden md:block" aria-hidden="true">&#8629; sign in</p>

                    <p class="text-xs text-center text-[var(--9x-ink)]/50 font-code">
                      default password is <code class="bg-[var(--9x-btn)] border border-[var(--9x-line)] px-1.5 py-0.5 rounded text-[var(--9x-ink)]">123456</code>
                    </p>
                    {#if hasPassword === false}
                      <p class="text-xs text-center text-[var(--9x-wait)] font-code">
                        Security risk: no password set. You will be asked to set one when logging in remotely.
                      </p>
                    {/if}
                  </form>
                {:else if errorMessage}
                  <p class="gl-error" role="alert">{errorMessage}</p>
                {/if}
              </div>
            {/if}

            <!-- Mobile: pills move under the panel; neutral line instead of the keyboard hint -->
            <p class="md:hidden font-code text-[10px] text-[var(--9x-ink)]/40 text-center mt-4 tracking-[0.14em]">
              9RouterX &middot; 9R Next Go Generation
            </p>
          </div>
      {/if}
      </div>

      {#if hasPassword !== null}
        <!-- Desktop >=1024px only: infrastructure status column -->
        <aside class="hidden lg:flex flex-col gap-2 pt-2 font-code text-[11px] text-[var(--9x-ink)]/45 shrink-0" aria-hidden="true">
          <span class="text-[var(--9x-ink)]/60 tracking-[0.14em]">9RouterX &middot; 9R Next Go Generation</span>
          <span>gateway &middot; <span class="text-[var(--9x-ok)]">ready</span></span>
          <span>region &middot; local</span>
          {#if version}<span>version &middot; v{version}</span>{/if}
        </aside>
      {/if}
    </div>
  </main>
</div>

<!--
  Glyph login styles. Scoped to this component; the tokens they read are
  defined once in web/src/index.css and rebind per theme (:root / .dark).
-->
<style>
  /* Frosted glass panel over the ASCII background */
  .gl-panel {
    background: var(--9x-glass);
    -webkit-backdrop-filter: blur(18px);
    backdrop-filter: blur(18px);
    border: 1px solid var(--9x-line);
    border-radius: 16px;
    box-shadow: var(--9x-shadow);
    padding: 24px;
  }
  @media (min-width: 768px) {
    .gl-panel {
      border-radius: 18px;
      padding: 32px;
    }
  }

  /* Pill: white-on-dark rounded chip, hairline border */
  .gl-pill {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    padding: 0 16px;
    border-radius: 999px;
    border: 1px solid var(--9x-line);
    background: var(--9x-btn);
    color: var(--9x-btn-ink);
    font-family: var(--font-code);
    font-size: 12px;
    text-decoration: none;
    transition: background-color 0.2s var(--9x-ease), transform 0.2s var(--9x-ease);
  }
  .gl-pill:hover {
    background: var(--9x-btn-hover);
  }
  @media (hover: hover) {
    .gl-pill:hover {
      transform: translateY(-1px);
      box-shadow: var(--9x-shadow-sm);
    }
  }
  .gl-pill:active {
    transform: translateY(0) scale(0.98);
  }
  .gl-pill:focus-visible {
    outline: 2px solid var(--9x-accent);
    outline-offset: 2px;
  }

  /* Input: dark surface, hairline border, mono — 16px font prevents iOS zoom.
     Depth (polish pass): a quiet inset recess at rest, and the accent halo +
     accent border on focus instead of the flat outline. */
  .gl-input {
    width: 100%;
    min-height: 44px;
    padding: 10px 12px;
    font-size: 16px;
    font-family: var(--font-code);
    color: var(--9x-ink);
    background: var(--9x-btn);
    border: 1px solid var(--9x-line);
    border-radius: 12px;
    outline: none;
    box-shadow: var(--9x-inset-rest);
    transition: border-color var(--9x-transition), box-shadow var(--9x-transition);
  }
  .gl-input::placeholder {
    color: color-mix(in srgb, var(--9x-ink) 65%, transparent);
  }
  /* Room for the visibility toggle inside the field */
  .gl-input-trailing {
    padding-right: 44px;
  }
  .gl-input:focus-visible {
    outline: none;
    border-color: var(--9x-accent);
    box-shadow: var(--9x-ring), var(--9x-inset-rest);
  }

  /* Primary button: btn surface, hover brightens and lifts 1px with the shadow
     growing a step; active presses it back down (transform/shadow only). */
  .gl-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    width: 100%;
    min-height: 44px;
    padding: 10px 16px;
    font-family: var(--font-headline);
    font-weight: 500;
    font-size: 14px;
    color: var(--9x-btn-ink);
    background: var(--9x-btn);
    border: 1px solid var(--9x-line);
    border-radius: 12px;
    cursor: pointer;
    transition: background-color var(--9x-transition), transform var(--9x-transition),
      box-shadow var(--9x-transition);
  }
  @media (hover: hover) {
    .gl-btn:hover:not(:disabled) {
      background: var(--9x-btn-hover);
      transform: translateY(-1px);
      box-shadow: var(--9x-shadow-md);
    }
  }
  .gl-btn:active:not(:disabled) {
    transform: translateY(0) scale(0.98);
    box-shadow: var(--9x-shadow-sm);
  }
  .gl-btn:focus-visible {
    outline: 2px solid var(--9x-accent);
    outline-offset: 2px;
  }
  .gl-btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  /* Keyboard hint */
  .gl-kbd-hint {
    font-family: var(--font-code);
    font-size: 11px;
    color: color-mix(in srgb, var(--9x-ink) 65%, transparent);
    text-align: center;
  }

  /* Error: mono, small, glyph red */
  .gl-error {
    font-family: var(--font-code);
    font-size: 12px;
    color: var(--9x-err);
    overflow-wrap: anywhere;
  }

  /* Gentle shake on a failed attempt (300ms, transform only) */
  .gl-shake {
    animation: gl-shake 0.3s var(--9x-ease);
  }
  @keyframes gl-shake {
    0%, 100% { transform: translateX(0); }
    25% { transform: translateX(-4px); }
    50% { transform: translateX(4px); }
    75% { transform: translateX(-2px); }
  }
  @media (prefers-reduced-motion: reduce) {
    .gl-shake {
      animation: none;
    }
  }

  /* Mono dots spinner: three dots pulsing in sequence */
  .gl-dots {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .gl-dots i {
    width: 4px;
    height: 4px;
    border-radius: 999px;
    background: currentColor;
    animation: gl-dot-pulse 1s infinite var(--9x-ease);
  }
  .gl-dots i:nth-child(2) {
    animation-delay: 0.15s;
  }
  .gl-dots i:nth-child(3) {
    animation-delay: 0.3s;
  }
  @keyframes gl-dot-pulse {
    0%, 100% { opacity: 0.25; }
    50% { opacity: 1; }
  }
</style>
