// TypedConfirm requires the user to type an exact phrase before the
// parent's submit button enables. Used in every destructive modal in
// the Fleet Security tab (ROTATE, LOCKOUT, LOCKDOWN, RECOVER, EXPORT).
//
// The phrase is intentionally short and uppercase so it's quick to
// type but visually distinct from accidental keypresses.

interface TypedConfirmProps {
  phrase: string
  value: string
  onChange: (v: string) => void
  // Optional helper text rendered above the input. Defaults to
  // "Type {phrase} to confirm" if not supplied.
  hint?: string
  disabled?: boolean
}

export default function TypedConfirm({
  phrase,
  value,
  onChange,
  hint,
  disabled,
}: TypedConfirmProps) {
  const ok = value === phrase
  return (
    <div className="space-y-1">
      <label className="block text-xs text-dark-300">
        {hint ?? `Type ${phrase} to confirm`}
      </label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value.toUpperCase())}
        disabled={disabled}
        autoComplete="off"
        spellCheck={false}
        placeholder={phrase}
        className={`w-full px-3 py-1.5 rounded bg-dark-900 border text-sm font-mono uppercase tracking-wider transition-colors ${
          ok
            ? 'border-amber-500 text-amber-300'
            : 'border-dark-600 text-dark-200 focus:border-primary-500'
        } focus:outline-none disabled:opacity-50`}
      />
    </div>
  )
}

// confirmed reports whether the supplied value equals the required
// phrase. Use as `disabled={!confirmed(phrase, value)}` on the parent's
// submit button.
export function confirmed(phrase: string, value: string): boolean {
  return value === phrase
}
