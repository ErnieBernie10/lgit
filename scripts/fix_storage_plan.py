from pathlib import Path
p=Path('internal/lgit/storage_policy.go')
s=p.read_text()
needle='''    stores,err:=trackedStore(root,p,"HEAD");if err!=nil{return a.fail(err)};if len(stores)==0{return 0}\n    c,err:=loadStorageConfig(root);if err!=nil{return a.fail(err)};id,err:=a.storageIdentity(root,c);if err!=nil{return a.fail(err)}\n'''
replacement='''    if _,headErr:=gitOutput(root,p.GitDir,"rev-parse","--verify","HEAD");headErr!=nil{return 0}\n    stores,err:=trackedStore(root,p,"HEAD");if err!=nil{return a.fail(err)};if len(stores)==0{return 0}\n    c,err:=loadStorageConfig(root);if err!=nil{return a.fail(err)};id,err:=a.storageIdentity(root,c);if err!=nil{return a.fail(err)}\n'''
if needle not in s:
    raise SystemExit('mixedStatus patch target not found')
s=s.replace(needle,replacement,1)
p.write_text(s)
Path('scripts/fix_storage_plan.py').unlink()
