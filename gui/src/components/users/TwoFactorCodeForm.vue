<script setup lang="ts">
// TwoFactorCodeForm.vue — Second step of a login on a two-factor account.
//
// Takes the challenge token issued by the first step (password, or an OIDC
// sign-in that came back with ?challenge=…) and completes the login with a
// TOTP code or a recovery code. Emits `verified` once the tokens are issued
// and `back` when the user returns to the credentials step.
import { computed, ref } from 'vue'
import { useForm, useField } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { useAuthStore } from '@/stores/auth'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
    Field,
    FieldError,
    FieldGroup,
    FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { PinInput, PinInputGroup, PinInputSlot } from '@/components/ui/pin-input'
import { AlertCircle, ArrowLeft, Loader2 } from '@lucide/vue'

const props = defineProps<{
    challengeToken: string
}>()

const emit = defineEmits<{
    verified: []
    back: []
}>()

// ─── Validation schema ────────────────────────────────────────────────────────

// Only the field of the selected method is checked.
const schema = toTypedSchema(
    z.object({
        useRecoveryCode: z.boolean(),
        pin: z.array(z.string()),
        recoveryCode: z.string(),
    }).superRefine((data, ctx) => {
        if (data.useRecoveryCode) {
            if (!data.recoveryCode.trim()) {
                ctx.addIssue({ code: 'custom', path: ['recoveryCode'], message: 'Enter your recovery code' })
            }
            return
        }
        const code = data.pin.join('')
        if (!code) {
            ctx.addIssue({ code: 'custom', path: ['pin'], message: 'Enter the 6-digit code' })
        } else if (code.length !== 6) {
            ctx.addIssue({ code: 'custom', path: ['pin'], message: 'Enter all 6 digits' })
        }
    }),
)

const { handleSubmit, isSubmitting, setFieldError } = useForm({
    validationSchema: schema,
    initialValues: { useRecoveryCode: false, pin: [] as string[], recoveryCode: '' },
})

const { value: useRecoveryCode } = useField<boolean>('useRecoveryCode')
const { value: pinValue, errorMessage: pinError } = useField<string[]>('pin')
const { value: recoveryCodeValue, errorMessage: recoveryCodeError } = useField<string>('recoveryCode')

// ─── State ────────────────────────────────────────────────────────────────────

const auth = useAuthStore()
const serverError = ref<string | null>(null)

const description = computed(() => useRecoveryCode.value
    ? 'Enter one of your recovery codes'
    : 'Enter the 6-digit code from your authenticator app')

// ─── Handlers ─────────────────────────────────────────────────────────────────

const onSubmit = handleSubmit(async (values) => {
    serverError.value = null
    const code = values.useRecoveryCode ? values.recoveryCode.trim() : values.pin.join('')
    try {
        await auth.completeTwoFactor(props.challengeToken, code)
        emit('verified')
    } catch {
        serverError.value = values.useRecoveryCode
            ? 'Invalid or already-used recovery code'
            : 'Invalid or expired code'
    }
})

function toggleMethod(): void {
    useRecoveryCode.value = !useRecoveryCode.value
    serverError.value = null
    setFieldError('pin', undefined)
    setFieldError('recoveryCode', undefined)
}
</script>

<template>
    <form class="p-6 md:p-8" novalidate @submit="onSubmit">
        <FieldGroup>
            <!-- Title -->
            <div class="flex flex-col items-center gap-2 text-center">
                <h1 class="text-2xl font-bold">Two-factor authentication</h1>
                <p class="text-sm text-muted-foreground text-balance">{{ description }}</p>
            </div>

            <!-- Server error -->
            <Transition enter-active-class="transition-all duration-200"
                enter-from-class="-translate-y-1 opacity-0"
                leave-active-class="transition-all duration-150"
                leave-to-class="-translate-y-1 opacity-0">
                <Alert v-if="serverError" variant="destructive">
                    <AlertCircle class="size-4" />
                    <AlertDescription>{{ serverError }}</AlertDescription>
                </Alert>
            </Transition>

            <!-- TOTP code -->
            <Field v-if="!useRecoveryCode">
                <PinInput v-model="pinValue" otp class="w-full" @complete="onSubmit()">
                    <PinInputGroup class="w-full justify-between">
                        <PinInputSlot v-for="i in 6" :key="i" :index="i - 1" />
                    </PinInputGroup>
                </PinInput>
                <FieldError v-if="pinError">{{ pinError }}</FieldError>
            </Field>

            <!-- Recovery code -->
            <Field v-else>
                <FieldLabel for="recovery-code">Recovery code</FieldLabel>
                <Input id="recovery-code" v-model="recoveryCodeValue" type="text"
                    placeholder="XXXX-XXXX-XXXX-XXXX" autocomplete="one-time-code" autofocus
                    spellcheck="false"
                    :class="recoveryCodeError ? 'border-destructive focus-visible:ring-destructive/30' : ''" />
                <FieldError v-if="recoveryCodeError">{{ recoveryCodeError }}</FieldError>
            </Field>

            <!-- Submit -->
            <Field>
                <Button type="submit" class="w-full" :disabled="isSubmitting">
                    <Loader2 v-if="isSubmitting" class="size-4 animate-spin" />
                    {{ isSubmitting ? 'Verifying…' : 'Verify' }}
                </Button>
            </Field>

            <!-- Toggle between TOTP and recovery code -->
            <button type="button"
                class="text-sm text-muted-foreground hover:text-foreground hover:underline"
                @click="toggleMethod">
                {{ useRecoveryCode ? 'Use your authenticator app instead' : 'Use a recovery code instead' }}
            </button>

            <!-- Back to sign in -->
            <button type="button"
                class="inline-flex items-center justify-center gap-1 text-sm text-muted-foreground hover:text-foreground"
                @click="emit('back')">
                <ArrowLeft class="size-4" />
                Back to sign in
            </button>
        </FieldGroup>
    </form>
</template>
