#!/usr/bin/env python3
"""Generate the foundation HTTP contract and its documentation copy using stdlib only."""
import json
import re
from pathlib import Path
root=Path(__file__).resolve().parents[1]
def ref(name):return {'$ref':'#/components/schemas/'+name}
def obj(props,required=None):return {'type':'object','additionalProperties':False,'properties':props,'required':list(props) if required is None else required}
def string(**kwargs):return {'type':'string',**kwargs}
def integer(**kwargs):return {'type':'integer',**kwargs}
def array(item):return {'type':'array','items':item}
def nullable(schema):return dict(schema,nullable=True)
S={}
for name,prefix in [('UserID','usr'),('WorkspaceID','ws'),('ProjectID','prj'),('BoardID','brd'),('JobID','job'),('InvitationID','inv')]:S[name]=string(pattern=f'^{prefix}_[0-9A-Za-z]{{27}}$',format='handdraw-resource-id',description='Canonical nonzero 160-bit KSUID; case sensitive. Regex is supplemented by the registered format validator.')
S['Revision']=string(pattern='^[1-9][0-9]*$',format='handdraw-revision',description='Positive signed int64 serialized as decimal text to retain JavaScript precision.')
S['Name']=string(minLength=1,maxLength=200,pattern=r'^\S(?:[\s\S]*\S)?$')
S['Role']=string(enum=['owner','editor','viewer'])
S['Capabilities']=obj({k:{'type':'boolean'} for k in ['can_read','can_edit_content','can_comment','can_export','can_manage_guests']})
S['Entitlement']=obj({'mode':string(enum=['unavailable','editable','read_only','purging']),'reason':string(),'grace_ends_at':nullable(string(format='date-time')),'access_expires_at':nullable(string(format='date-time')),'retention_ends_at':nullable(string(format='date-time'))})
S['Me']=obj({'id':ref('UserID'),'display_name':string(minLength=1,maxLength=200),'email':string(format='email')})
S['Workspace']=obj({'id':ref('WorkspaceID'),'name':ref('Name'),'kind':string(enum=['personal','team']),'role':ref('Role'),'revision':ref('Revision'),'access_revision':ref('Revision'),'lifecycle':string(enum=['ready','purging','deleted']),'capabilities':ref('Capabilities'),'entitlement':ref('Entitlement'),'created_at':string(format='date-time'),'updated_at':string(format='date-time')})
S['Project']=obj({'id':ref('ProjectID'),'workspace_id':ref('WorkspaceID'),'name':ref('Name'),'revision':ref('Revision'),'created_at':string(format='date-time'),'updated_at':string(format='date-time')})
S['Board']=obj({'id':ref('BoardID'),'workspace_id':ref('WorkspaceID'),'project_id':nullable({'allOf':[ref('ProjectID')]}),'name':ref('Name'),'status':string(enum=['initializing','active','deleting']),'revision':ref('Revision'),'document_schema_version':integer(minimum=1),'capabilities':ref('Capabilities'),'created_at':string(format='date-time'),'updated_at':string(format='date-time')})
S['DeletionJob']=obj({'id':ref('JobID'),'board_id':ref('BoardID'),'status':string(enum=['queued','running','succeeded','failed','canceled'])})
S['CreateWorkspace']=obj({'name':ref('Name'),'kind':string(enum=['personal','team'])})
S['CreateProject']=obj({'name':ref('Name')})
S['Rename']=obj({'name':ref('Name')})
S['CreateBoard']=obj({'name':ref('Name'),'project_id':nullable({'allOf':[ref('ProjectID')]}),'initialization':string(enum=['empty','get_started'])})
S['PatchBoard']=obj({'name':ref('Name'),'project_id':nullable({'allOf':[ref('ProjectID')]})},[]);S['PatchBoard']['minProperties']=1
S['PendingOperation']=obj({'operation':string(minLength=1,maxLength=120),'status':string(enum=['processing'])})
S['Meta']=obj({'request_id':string(minLength=1),'pagination':obj({'next_cursor':nullable(string(maxLength=4096))})},['request_id'])
S['Error']=obj({'success':{'type':'boolean','enum':[False]},'meta':ref('Meta'),'error':obj({'code':string(minLength=1),'message':string(minLength=1),'details':{'type':'object','additionalProperties':True}},['code','message'])})
for name in ['Me','Workspace','Project','Board','DeletionJob','PendingOperation']:
 S[name+'Response']=obj({'success':{'type':'boolean','enum':[True]},'meta':ref('Meta'),'result':ref(name)})
for name in ['Workspace','Project','Board']:
 S[name+'ListResponse']=obj({'success':{'type':'boolean','enum':[True]},'meta':obj({'request_id':string(minLength=1),'pagination':obj({'next_cursor':nullable(string(maxLength=4096))})}),'result':array(ref(name))})
S['Member']=obj({'user_id':ref('UserID'),'display_name':string(),'role':ref('Role'),'revision':ref('Revision')})
S['Invite']=obj({'email':string(format='email',maxLength=254),'role':string(enum=['editor','viewer'])})
S['InviteGuest']=obj({'email':string(format='email',maxLength=254),'role':string(enum=['viewer'])})
S['ChangeMember']=obj({'role':string(enum=['editor','viewer'])})
S['Invitation']=obj({'id':ref('InvitationID'),'workspace_id':ref('WorkspaceID'),'board_id':nullable({'allOf':[ref('BoardID')]}),'scope':string(enum=['workspace','board']),'email':string(format='email'),'role':string(enum=['editor','viewer']),'status':string(enum=['pending','accepted','revoked','expired']),'expires_at':string(format='date-time'),'created_at':string(format='date-time')})
S['InvitationSubmission']=obj({'invitation':ref('Invitation'),'delivery_status':string(enum=['pending_integration'],description='Invitation persisted; email provider integration is not enabled. No email has been sent.')})
S['AcceptInvitation']=obj({'token':string(pattern='^[A-Za-z0-9_-]{43}$',writeOnly=True,description='Single-use 256-bit secret. Send only in this POST body; never log it or place it in a URL.')})
S['AcceptedAccess']=obj({'workspace_id':ref('WorkspaceID'),'board_id':nullable({'allOf':[ref('BoardID')]}),'user_id':ref('UserID'),'role':ref('Role'),'source':string(enum=['workspace','board_grant'])})
S['Guest']=obj({'user_id':ref('UserID'),'display_name':string(),'role':string(enum=['viewer']),'created_at':string(format='date-time'),'expires_at':nullable(string(format='date-time'))})
S['GuestRemoval']=obj({'removed':{'type':'boolean','enum':[True]},'effective_access':nullable(obj({'role':ref('Role'),'source':string(enum=['workspace'])}))})
for name in ['Member','InvitationSubmission','AcceptedAccess','GuestRemoval']:
 S[name+'Response']=obj({'success':{'type':'boolean','enum':[True]},'meta':ref('Meta'),'result':ref(name)})
for name in ['Member','Invitation']:
 S[name+'ListResponse']=obj({'success':{'type':'boolean','enum':[True]},'meta':obj({'request_id':string(minLength=1),'pagination':obj({'next_cursor':nullable(string(maxLength=4096))})}),'result':array(ref(name))})
S['GuestListResponse']=obj({'success':{'type':'boolean','enum':[True]},'meta':ref('Meta'),'result':array(ref('Guest'))})
P={'Limit':{'name':'limit','in':'query','schema':integer(minimum=1,maximum=100,default=50)},'Cursor':{'name':'cursor','in':'query','schema':string(minLength=1,maxLength=4096),'description':'Opaque authenticated cursor bound to actor, workspace, resource, filter and the operation-specific ordering described below; mismatch is invalid_cursor.'},'IfMatch':{'name':'If-Match','in':'header','required':True,'schema':string(pattern='^"[1-9][0-9]*"$',format='handdraw-etag'),'description':'Strong quoted decimal metadata revision. Missing: 428. Stale: 412. Never the CRDT revision.'},'IdempotencyKey':{'name':'Idempotency-Key','in':'header','required':True,'schema':string(format='handdraw-idempotency-key'),'description':'Nonzero client UUID, scoped to actor+operation. Same normalized payload retries reuse the result after current authorization. Changed payload: 409; in progress: 202; 24-hour record TTL.'}}
for param,typ in [('workspace_id','WorkspaceID'),('project_id','ProjectID'),('board_id','BoardID'),('user_id','UserID'),('invitation_id','InvitationID')]:P[param]={'name':param,'in':'path','required':True,'schema':ref(typ)}
headers={'X-Request-ID':{'schema':string()},'Cache-Control':{'schema':string(enum=['private, no-store'])}}
def response(schema=None,description='Success',etag=False):
 r={'description':description,'headers':dict(headers)}
 if etag:r['headers']['ETag']={'schema':string(pattern='^"[1-9][0-9]*"$')}
 if schema:r['content']={'application/json':{'schema':ref(schema)}}
 return r
errors={'400':'invalid_request / invalid_cursor / invalid_resource_id','401':'unauthenticated','403':'permission_denied / entitlement_read_only','404':'not_found (including inaccessible IDs)','409':'idempotency_conflict / project_not_empty / board_initializing / membership_conflict','410':'invitation_gone','412':'revision_conflict','413':'payload_too_large','415':'unsupported_media_type','422':'invalid_document_schema','428':'precondition_required','429':'rate_limited','503':'identity_unavailable / dependency_unavailable / save_unavailable'}
R={code:response('Error',desc) for code,desc in errors.items()};R['429']['headers']['Retry-After']={'schema':string(pattern='^[0-9]+$')}
paths={}
def op(path,method,name,out,status='200',body=None,paged=False,description='',idempotent=None,precondition=None):
 use_key = method=='post' if idempotent is None else idempotent
 use_revision = method in ('patch','delete') if precondition is None else precondition
 parameters=[{'$ref':'#/components/parameters/'+param} for param in ['workspace_id','project_id','board_id','user_id','invitation_id'] if '{'+param+'}' in path]
 if paged:parameters += [{'$ref':'#/components/parameters/Limit'},{'$ref':'#/components/parameters/Cursor'}]
 if use_key:parameters += [{'$ref':'#/components/parameters/IdempotencyKey'}]
 if use_revision:parameters += [{'$ref':'#/components/parameters/IfMatch'}]
 responses={status:response(out,etag=not paged and out in ['WorkspaceResponse','ProjectResponse','BoardResponse','MemberResponse'])}
 for code in ['400','401','403','404','429','503']:responses[code]={'$ref':'#/components/responses/'+code}
 if method in ('post','patch','delete'):
  for code in ['409','413','415','422']:responses[code]={'$ref':'#/components/responses/'+code}
 if use_revision:
  for code in ['412','428']:responses[code]={'$ref':'#/components/responses/'+code}
 if use_key:responses['202']=response('PendingOperationResponse','Operation already processing; result contains a safe operation reference. No replay of cached secrets.')
 operation={'operationId':name,'summary':re.sub(r'(?<!^)([A-Z])', r' \1', name).capitalize(),'parameters':parameters,'responses':responses,'x-max-request-bytes':65536}
 if body:operation['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
 available=True
 category = 'Invitations' if 'invitations' in path else 'Members' if 'members' in path else 'Guests' if 'guests' in path else 'Boards' if 'boards' in path else 'Projects' if 'projects' in path else 'Workspaces' if 'workspaces' in path else 'Identity'
 operation['tags']=[category]
 operation['x-implementation-status']='available-when-enabled' if available else 'planned'
 operation['description']=('Available when the corresponding runtime feature is enabled.' if available else 'Planned; not mounted by the server yet.') + (' '+description if description else '')
 paths.setdefault(path,{})[method]=operation
op('/v1/me','get','getMe','MeResponse')
op('/v1/me/shared-boards','get','listSharedBoards','BoardListResponse',paged=True)
op('/v1/workspaces','get','listWorkspaces','WorkspaceListResponse',paged=True)
op('/v1/workspaces','post','createWorkspace','WorkspaceResponse','201','CreateWorkspace',description='Atomically create workspace + Owner; subscription is separate and starts unavailable.')
op('/v1/workspaces/{workspace_id}','get','getWorkspace','WorkspaceResponse')
op('/v1/workspaces/{workspace_id}','patch','renameWorkspace','WorkspaceResponse',body='Rename')
op('/v1/workspaces/{workspace_id}/projects','get','listProjects','ProjectListResponse',paged=True)
op('/v1/workspaces/{workspace_id}/projects','post','createProject','ProjectResponse','201','CreateProject')
op('/v1/projects/{project_id}','patch','renameProject','ProjectResponse',body='Rename')
op('/v1/projects/{project_id}','delete','deleteEmptyProject',None,'204',description='Reject nonempty projects; repeat delete with parent access has no additional effect.')
op('/v1/workspaces/{workspace_id}/boards','get','listBoards','BoardListResponse',paged=True)
paths['/v1/workspaces/{workspace_id}/boards']['get']['parameters'].append({'name':'project_id','in':'query','schema':string(pattern='^(ungrouped|prj_[0-9A-Za-z]{27})$'),'description':'Omitted: all groups. ungrouped: no project. Otherwise canonical prj_ ID; part of cursor scope.'})
op('/v1/workspaces/{workspace_id}/boards','post','createBoard','BoardResponse','201','CreateBoard',description='Atomically create board and server-built schema-1 document. Import initialization is unavailable until T08.')
op('/v1/boards/{board_id}','get','getBoard','BoardResponse')
op('/v1/boards/{board_id}','patch','updateBoard','BoardResponse',body='PatchBoard')
op('/v1/boards/{board_id}','delete','deleteBoard','DeletionJobResponse','202',description='Mark deleting and enqueue cleanup atomically before closing the room.')
op('/v1/boards/{board_id}/document','get','getBoardDocument',None,description='Read committed schema-1 Yjs update bytes; no offline or mutable REST document writes.')
paths['/v1/boards/{board_id}/document']['get']['responses']['200']['content']={'application/octet-stream':{'schema':{'type':'string','format':'binary'}}}
paths['/v1/boards/{board_id}/document']['get']['responses']['200']['headers']['X-Document-Schema-Version']={'schema':{'type':'integer','enum':[1]}}
paths['/v1/boards/{board_id}']['delete']['description']='Enabled only with the in-process restricted cleanup worker. Returns the same durable job on authorized repeated deletion.'
op('/v1/workspaces/{workspace_id}/members','get','listMembers','MemberListResponse',paged=True,description='List the workspace roster without exposing Auth email addresses.')
op('/v1/workspaces/{workspace_id}/members/{user_id}','patch','changeMemberRole','MemberResponse',body='ChangeMember',description='Owner changes Editor/Viewer role using the current member revision and atomic seat capacity checks.')
op('/v1/workspaces/{workspace_id}/members/{user_id}','delete','removeMember',None,'204',description='Remove a non-Owner member; an authorized repeat has no further effect.')
op('/v1/workspaces/{workspace_id}/invitations','post','inviteMember','InvitationSubmissionResponse','202','Invite',description='Owner reserves Team Editor capacity and records a seven-day recipient-bound invitation; email delivery is pending integration.')
op('/v1/workspaces/{workspace_id}/invitations','get','listInvitations','InvitationListResponse',paged=True,description='Owner lists invitation history, including calculated expiry.')
op('/v1/boards/{board_id}/invitations','post','inviteBoardGuest','InvitationSubmissionResponse','202','InviteGuest',description='Owner records a Viewer-only invitation for one Cloud/Team board; email delivery is pending integration.')
for path in ['/v1/workspaces/{workspace_id}/invitations','/v1/boards/{board_id}/invitations']:
 paths[path]['post']['responses']['202']=response('InvitationSubmissionResponse','Invitation persisted; email delivery remains pending integration. Concurrent processing may return PendingOperationResponse.')
 paths[path]['post']['responses']['202']['content']['application/json']['schema']={'oneOf':[ref('InvitationSubmissionResponse'),ref('PendingOperationResponse')]}
op('/v1/invitations/accept','post','acceptInvitation','AcceptedAccessResponse',body='AcceptInvitation',idempotent=False,description='Consume one token after matching the current confirmed Auth email and rechecking entitlement/capacity. Repeat use returns 410; inspect current access after a lost response.')
paths['/v1/invitations/accept']['post']['responses']['410']={'$ref':'#/components/responses/410'}
op('/v1/invitations/{invitation_id}','delete','revokeInvitation',None,'204',precondition=False,description='Owner revokes a pending invitation and releases reserved capacity.')
op('/v1/boards/{board_id}/guests','get','listBoardGuests','GuestListResponse',description='Owner lists explicit Viewer grants, independently of workspace membership.')
op('/v1/boards/{board_id}/guests/{user_id}','delete','removeBoardGuest','GuestRemovalResponse',precondition=False,description='Owner removes the board grant and returns any remaining workspace membership.')
for path,methods in paths.items():
 for method,operation in methods.items():
  if operation['tags'][0] in ['Members','Invitations','Guests'] or path=='/v1/me/shared-boards':
   operation['description']='Available with APP_MEMBERSHIP_ENABLED, board/workspace/identity enabled and schema version 9. '+operation['description']
  if operation.get('operationId') in ['listMembers','listSharedBoards']:
   operation['description']+=' Cursor ordering: resource ID ascending.'
  if operation.get('operationId')=='listInvitations':
   operation['description']+=' Cursor ordering: created_at descending, then invitation ID descending.'
doc={'openapi' :'3.0.3','info':{'title':'Handdraw API','version':'1.0.0','description':'Handdraw API contract. Identity, workspace onboarding and project/board routes are wired behind explicit configuration. Board deletion additionally requires the cleanup worker. Team membership, invitations and board Viewer grants require the membership feature flag. Invitation email delivery remains pending integration. Bearer identity is verified before profile resolution. Unknown fields/trailing JSON are rejected. Future domain endpoints are added with their implementation contracts.'},'servers':[{'url':'http://127.0.0.1:8081','description':'Local development; deployment must use HTTPS.'}],'security':[{'supabaseBearer':[]}],'tags':[{'name':name,'description':description} for name,description in [('Identity','Current authenticated profile.'),('Workspaces','Workspace onboarding and metadata.'),('Projects','Board organization within a workspace.'),('Boards','Board metadata and committed document content.'),('Members','Team membership and role changes.'),('Invitations','Recipient-bound workspace and board invitations.'),('Guests','Board-scoped Viewer access.')]],'paths':paths,'components':{'securitySchemes':{'supabaseBearer':{'type':'http','scheme':'bearer','bearerFormat':'Supabase JWT'}},'schemas':S,'parameters':P,'responses':R}}
text=json.dumps(doc,indent=2,ensure_ascii=False)+'\n'
(root/'contracts/openapi.json').write_text(text)
(root.parent/'docs/architecture/openapi.yaml').write_text(text)
