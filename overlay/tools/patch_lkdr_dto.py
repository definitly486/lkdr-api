#!/usr/bin/env python3
"""Apply the LKDR fiscal-error compatibility changes to an upstream checkout."""
from pathlib import Path
import sys

root = Path(sys.argv[1] if len(sys.argv) > 1 else ".").resolve()
dto_path = root / "dto.go"
test_path = root / "dto_test.go"

dto = dto_path.read_text(encoding="utf-8")
old_const = '\tReceiptFiscalDataNotFound ErrorCode = "receipt.fiscaldata.not.found.dr"'
new_const = old_const + '\n\tReceiptFiscalDataNotFoundV2 ErrorCode = "receipt.fiscal.data.not.found"\n\tReceiptFiscalDataInvalidKKT ErrorCode = "receipt.fiscal.data.invalid.kkt"'
if "ReceiptFiscalDataNotFoundV2" not in dto:
    if old_const not in dto:
        raise SystemExit("Не найден ожидаемый блок констант в dto.go; файл не изменён.")
    dto = dto.replace(old_const, new_const, 1)

old_func = '''func IsDataNotFound(err error) bool {
\tvar e Error
\tif errors.As(err, &e); e.Code == ReceiptFiscalDataNotFound {
\t\treturn true
\t}

\treturn false
}'''
new_func = '''func IsDataNotFound(err error) bool {
\tvar e Error
\tif !errors.As(err, &e) {
\t\treturn false
\t}
\treturn e.Code == ReceiptFiscalDataNotFound || e.Code == ReceiptFiscalDataNotFoundV2
}

func IsInvalidKKT(err error) bool {
\tvar e Error
\treturn errors.As(err, &e) && e.Code == ReceiptFiscalDataInvalidKKT
}'''
if "func IsInvalidKKT(" not in dto:
    if old_func not in dto:
        raise SystemExit("Не найдена ожидаемая функция IsDataNotFound в dto.go; файл не изменён.")
    dto = dto.replace(old_func, new_func, 1)
dto_path.write_text(dto, encoding="utf-8")

tests = test_path.read_text(encoding="utf-8")
if "func TestIsDataNotFoundNewCodes(" not in tests:
    tests += '''

func TestIsDataNotFoundNewCodes(t *testing.T) {
\ttests := []struct {
\t\tname string
\t\terr  error
\t\twant bool
\t}{
\t\t{name: "old code", err: Error{Code: ReceiptFiscalDataNotFound}, want: true},
\t\t{name: "new code", err: Error{Code: ReceiptFiscalDataNotFoundV2}, want: true},
\t\t{name: "invalid KKT", err: Error{Code: ReceiptFiscalDataInvalidKKT}, want: false},
\t}
\tfor _, tt := range tests {
\t\tt.Run(tt.name, func(t *testing.T) {
\t\t\tif got := IsDataNotFound(tt.err); got != tt.want {
\t\t\t\tt.Errorf("IsDataNotFound() = %v, want %v", got, tt.want)
\t\t\t}
\t\t})
\t}
}

func TestIsInvalidKKT(t *testing.T) {
\tif !IsInvalidKKT(Error{Code: ReceiptFiscalDataInvalidKKT}) {
\t\tt.Fatal("IsInvalidKKT() = false for invalid KKT error")
\t}
\tif IsInvalidKKT(Error{Code: ReceiptFiscalDataNotFoundV2}) {
\t\tt.Fatal("IsInvalidKKT() = true for not found error")
\t}
}
'''
    test_path.write_text(tests, encoding="utf-8")
print("Исправления кодов ошибок ФД применены.")
