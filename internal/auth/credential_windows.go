package auth

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	procCredRead      = advapi32.NewProc("CredReadW")
	procCredWrite     = advapi32.NewProc("CredWriteW")
	procCredDelete    = advapi32.NewProc("CredDeleteW")
	procCredFree      = advapi32.NewProc("CredFree")
)

const credTypeGeneric = 1

type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        uint64
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func targetName(service, account string) string {
	return service + "/" + account
}

func credentialGet(service, account string) (string, error) {
	target, _ := syscall.UTF16PtrFromString(targetName(service, account))
	var cred *credential
	ret, _, err := procCredRead.Call(
		uintptr(unsafe.Pointer(target)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	if ret == 0 {
		return "", fmt.Errorf("credential manager: %w", err)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))

	blob := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
	return strings.TrimRight(string(blob), "\x00"), nil
}

func credentialSet(service, account, secret string) error {
	target, _ := syscall.UTF16PtrFromString(targetName(service, account))
	user, _ := syscall.UTF16PtrFromString(account)
	blobBytes := []byte(secret)

	cred := credential{
		Type:               credTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blobBytes)),
		CredentialBlob:     &blobBytes[0],
		Persist:            2, // CRED_PERSIST_LOCAL_MACHINE
		UserName:           user,
	}

	ret, _, err := procCredWrite.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if ret == 0 {
		return fmt.Errorf("credential manager: %w", err)
	}
	return nil
}

func credentialDelete(service, account string) error {
	target, _ := syscall.UTF16PtrFromString(targetName(service, account))
	ret, _, err := procCredDelete.Call(
		uintptr(unsafe.Pointer(target)),
		credTypeGeneric,
		0,
	)
	if ret == 0 {
		return fmt.Errorf("credential manager: %w", err)
	}
	return nil
}
