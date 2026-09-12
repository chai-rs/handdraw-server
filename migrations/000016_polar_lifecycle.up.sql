BEGIN;

GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;

CREATE OR REPLACE FUNCTION handdraw.polar_billing_prepare(i text) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.billing_change_intents%ROWTYPE; sid text;
BEGIN
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i;
 IF NOT FOUND OR r.status NOT IN ('requested','awaiting_provider','scheduled','applied') THEN RAISE EXCEPTION 'invalid operation' USING ERRCODE='HD409'; END IF;
 UPDATE handdraw.billing_change_intents SET provider='polar' WHERE id=i AND provider<>'polar';
 SELECT coalesce(
  (SELECT e.payload#>>'{data,id}' FROM handdraw.polar_webhook_events e WHERE e.event_type LIKE 'subscription.%' AND
   e.payload#>>'{data,metadata,intent_id}'=i ORDER BY e.occurred_at DESC,e.provider_event_id DESC LIMIT 1),
  (SELECT c.provider_subscription_id FROM handdraw.billing_contracts c WHERE c.intent_id=i ORDER BY c.updated_at DESC LIMIT 1),
  CASE WHEN r.kind NOT IN ('plan_upgrade','plan_downgrade') THEN
   (SELECT e.payload#>>'{data,id}' FROM handdraw.polar_webhook_events e WHERE e.event_type LIKE 'subscription.%' AND e.payload#>>'{data,id}'=r.source_subscription_id ORDER BY e.occurred_at DESC,e.provider_event_id DESC LIMIT 1)
  END,
  r.source_subscription_id
 ) INTO sid;
 RETURN jsonb_build_object('workspace_id',r.workspace_id,'kind',r.kind,'status',r.status,'phase',r.phase,'plan',r.target_plan,'billing_interval',r.billing_interval,'seats',r.target_seats,'source_seats',coalesce(r.source_seats,0),'source_subscription_id',coalesce(r.source_subscription_id,''),'subscription_id',coalesce(sid,''),'checkout_id',coalesce(r.provider_checkout_id,''),'checkout_url',coalesce(r.checkout_url,''));
END; $$;

CREATE FUNCTION handdraw.polar_portal_customer(w text) RETURNS text LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path='' AS $$
DECLARE customer text;
BEGIN
 IF NOT handdraw.is_workspace_owner(w) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 SELECT provider_customer_id INTO customer FROM handdraw.subscriptions WHERE workspace_id=w AND provider='polar';
 IF customer IS NULL OR customer='' THEN RAISE EXCEPTION 'billing customer not found' USING ERRCODE='HD404'; END IF;
 RETURN w;
END; $$;

CREATE FUNCTION handdraw.apply_polar_billing(i text,token text,p jsonb) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.billing_change_intents%ROWTYPE; ws handdraw.workspaces%ROWTYPE; s handdraw.subscriptions%ROWTYPE; previous handdraw.billing_contracts%ROWTYPE;
 sid text:=p->>'subscription_id'; ver bigint:=(p->>'version')::bigint; state text:=p->>'status'; deadline timestamptz; failure timestamptz;
 item jsonb; order_key text; refunded bigint; first_trial boolean;
BEGIN
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 SELECT * INTO ws FROM handdraw.workspaces WHERE id=r.workspace_id FOR UPDATE;
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i FOR UPDATE;
 IF r.lease_token IS DISTINCT FROM token OR r.lease_until<=clock_timestamp() THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='HD409'; END IF;
 UPDATE handdraw.billing_change_intents SET lease_token=NULL,lease_until=NULL,retry_at=clock_timestamp()+interval '30 seconds' WHERE id=i;
 IF r.provider<>'polar' OR p->>'provider'<>'polar' OR p->>'intent_id'<>i OR p->>'workspace_id'<>r.workspace_id OR p->>'plan'<>r.target_plan OR p->>'billing_interval'<>r.billing_interval OR (p->>'seats')::integer<>r.target_seats OR sid!~'^[0-9a-f-]{36}$' OR coalesce(p->>'customer_id','')='' OR coalesce(p->>'product_id','')='' THEN RAISE EXCEPTION 'unverified Polar snapshot' USING ERRCODE='HD409'; END IF;
 UPDATE handdraw.polar_webhook_events SET status='processed',lease_token=NULL,lease_until=NULL,last_error=NULL,processed_at=clock_timestamp()
 WHERE status<>'processed' AND (
  payload#>>'{data,metadata,intent_id}'=i OR payload#>>'{data,id}'=sid OR
  payload#>>'{data,subscription_id}'=sid OR payload#>>'{data,subscription,id}'=sid OR
  payload#>>'{data,checkout_id}'=r.provider_checkout_id
 );
 SELECT * INTO s FROM handdraw.subscriptions WHERE workspace_id=r.workspace_id;
 SELECT * INTO previous FROM handdraw.billing_contracts WHERE provider_subscription_id=sid;
 IF previous.version>ver THEN RETURN; END IF;
 IF state NOT IN ('pending','trialing','active','renewal_failed','ended','failed') THEN RAISE EXCEPTION 'invalid state' USING ERRCODE='HD400'; END IF;
 first_trial:=s.workspace_id IS NULL AND NOT EXISTS(SELECT 1 FROM handdraw.billing_contracts WHERE workspace_id=r.workspace_id AND snapshot->>'status' IN ('trialing','active'));
 INSERT INTO handdraw.billing_contracts(provider_subscription_id,workspace_id,intent_id,version,snapshot) VALUES(sid,r.workspace_id,i,ver,p)
 ON CONFLICT(provider_subscription_id) DO UPDATE SET version=excluded.version,snapshot=excluded.snapshot,updated_at=clock_timestamp();
 FOR item IN SELECT value FROM jsonb_array_elements(p->'orders') LOOP
  INSERT INTO handdraw.payment_orders(id,workspace_id,provider,provider_order_id,provider_subscription_id,currency,amount_minor,paid_at,service_from,service_until)
  VALUES(item->>'id',r.workspace_id,'polar',item->>'provider_order_id',sid,'USD',(item->>'amount_minor')::bigint,(item->>'paid_at')::timestamptz,(item->>'service_from')::timestamptz,(item->>'service_until')::timestamptz) ON CONFLICT(provider,provider_order_id) DO NOTHING;
  IF NOT EXISTS(SELECT 1 FROM handdraw.payment_orders WHERE provider='polar' AND provider_order_id=item->>'provider_order_id' AND workspace_id=r.workspace_id AND provider_subscription_id=sid AND amount_minor=(item->>'amount_minor')::bigint AND paid_at=(item->>'paid_at')::timestamptz AND service_from=(item->>'service_from')::timestamptz AND service_until=(item->>'service_until')::timestamptz) THEN RAISE EXCEPTION 'order identity conflict' USING ERRCODE='HD409'; END IF;
 END LOOP;
 FOR item IN SELECT value FROM jsonb_array_elements(p->'refunds') LOOP
  SELECT id INTO order_key FROM handdraw.payment_orders WHERE provider_order_id=item->>'provider_order_id' AND provider_subscription_id=sid FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'refund order missing' USING ERRCODE='HD409'; END IF;
  INSERT INTO handdraw.payment_refunds(id,order_id,provider_refund_id,amount_minor,refunded_at) VALUES(item->>'id',order_key,item->>'provider_refund_id',(item->>'amount_minor')::bigint,(item->>'refunded_at')::timestamptz) ON CONFLICT(provider_refund_id) DO NOTHING;
  IF NOT EXISTS(SELECT 1 FROM handdraw.payment_refunds WHERE provider_refund_id=item->>'provider_refund_id' AND order_id=order_key AND amount_minor=(item->>'amount_minor')::bigint) THEN RAISE EXCEPTION 'refund identity conflict' USING ERRCODE='HD409'; END IF;
  SELECT sum(amount_minor) INTO refunded FROM handdraw.payment_refunds WHERE order_id=order_key;
  IF refunded>(SELECT amount_minor FROM handdraw.payment_orders WHERE id=order_key) THEN RAISE EXCEPTION 'refund exceeds order' USING ERRCODE='HD409'; END IF;
 END LOOP;
 -- Money history survives late delivery, but a purging workspace or superseded contract cannot regain content access.
 IF ws.lifecycle<>'ready' OR ws.deleted_at IS NOT NULL THEN UPDATE handdraw.billing_change_intents SET phase='payment_requires_review' WHERE id=i; RETURN; END IF;
 IF r.status='applied' AND s.provider_subscription_id IS DISTINCT FROM sid THEN RETURN; END IF;
 IF previous.version=ver AND r.status='applied' THEN RETURN; END IF;
 IF r.actor_user_id<>ws.owner_user_id THEN UPDATE handdraw.billing_change_intents SET phase='confirmation_required' WHERE id=i; RETURN; END IF;
 IF r.kind IN ('cancel','resume') THEN
  IF coalesce((p->>'cancel_at_period_end')::boolean,false) IS DISTINCT FROM (r.kind='cancel') THEN RAISE EXCEPTION 'provider cancellation mismatch' USING ERRCODE='HD409'; END IF;
  IF s.provider_subscription_id IS DISTINCT FROM r.source_subscription_id OR (s.revision<>r.source_revision AND s.cancel_at_period_end IS DISTINCT FROM (r.kind='cancel')) THEN UPDATE handdraw.billing_change_intents SET status='failed',phase='source_changed' WHERE id=i; RETURN; END IF;
  UPDATE handdraw.subscriptions SET cancel_at_period_end=(r.kind='cancel'),revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id;
  UPDATE handdraw.billing_change_intents SET status='applied',phase='complete' WHERE id=i; RETURN;
 END IF;
 IF r.kind='plan_downgrade' AND r.status='scheduled' AND r.phase='awaiting_team_end' THEN
  IF NOT coalesce((p->>'cancel_at_period_end')::boolean,false) THEN RAISE EXCEPTION 'provider cancellation mismatch' USING ERRCODE='HD409'; END IF;
  IF s.provider_subscription_id IS DISTINCT FROM r.source_subscription_id THEN RETURN; END IF;
  IF NOT s.cancel_at_period_end THEN UPDATE handdraw.subscriptions SET cancel_at_period_end=true,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id; END IF;
  RETURN;
 END IF;
 IF state IN ('pending','failed') THEN
  IF r.status='requested' THEN UPDATE handdraw.billing_change_intents SET status='awaiting_provider',phase='awaiting_payment' WHERE id=i; END IF;
  RETURN;
 END IF;
 IF r.status<>'applied' THEN
  IF r.confirmed_at IS NULL OR ((r.access_revision<>ws.access_revision OR r.source_revision<>coalesce(s.revision,0)) AND NOT (r.kind='seat_change' AND r.target_seats<r.source_seats AND r.effective_at<=clock_timestamp() AND s.provider_subscription_id IS NOT DISTINCT FROM r.source_subscription_id AND s.paid_seats=r.source_seats AND s.plan=r.source_plan AND s.billing_interval=r.billing_interval AND s.paid_through_at=r.source_period_end AND s.grace_ends_at IS NULL AND s.status IN ('active','ended') AND r.access_snapshot=handdraw.billing_access_snapshot(r.workspace_id))) THEN UPDATE handdraw.billing_change_intents SET phase='confirmation_required' WHERE id=i; RETURN; END IF;
  IF r.kind='plan_downgrade' THEN
   IF s.status<>'ended' OR coalesce(s.access_expires_at,s.paid_through_at,s.trial_ends_at)>clock_timestamp() OR r.member_changes<>handdraw.billing_removals(r.workspace_id) OR state<>'active' THEN UPDATE handdraw.billing_change_intents SET phase='confirmation_required' WHERE id=i; RETURN; END IF;
  END IF;
 END IF;
 IF state='trialing' THEN
  deadline:=(p->>'trial_ends_at')::timestamptz;
  IF NOT coalesce((p->>'card_verified')::boolean,false) OR deadline IS NULL OR deadline<=clock_timestamp() OR r.target_seats>5 OR
   (r.trial_ends_at IS NOT NULL AND deadline<>r.trial_ends_at) OR (r.trial_ends_at IS NULL AND (NOT first_trial AND s.provider_subscription_id IS DISTINCT FROM sid OR deadline>r.created_at+interval '7 days')) THEN RAISE EXCEPTION 'trial not eligible' USING ERRCODE='HD409'; END IF;
 ELSIF state='active' THEN
  deadline:=(p->>'paid_through_at')::timestamptz;
  IF deadline IS NULL OR deadline<=clock_timestamp() OR (s.provider_subscription_id=sid AND deadline<s.paid_through_at) OR (r.kind='seat_change' AND r.target_seats>=r.source_seats AND r.trial_ends_at IS NULL AND r.status<>'applied' AND deadline IS DISTINCT FROM r.source_period_end) OR NOT EXISTS(SELECT 1 FROM handdraw.payment_orders WHERE provider_subscription_id=sid AND paid_at<=clock_timestamp() AND service_until=deadline AND amount_minor>=CASE WHEN r.status='applied' OR r.trial_ends_at IS NOT NULL OR r.target_seats<r.source_seats THEN r.recurring_minor ELSE r.amount_minor END) THEN RAISE EXCEPTION 'payment evidence missing' USING ERRCODE='HD409'; END IF;
 ELSE
  IF s.provider_subscription_id IS DISTINCT FROM sid THEN RETURN; END IF;
  IF state='renewal_failed' AND s.last_successful_payment_at IS NOT NULL THEN
   failure:=coalesce(s.renewal_failure_at,(p->>'renewal_failure_at')::timestamptz);
   IF failure IS NULL OR failure>clock_timestamp() OR failure<s.paid_through_at OR coalesce(p->>'renewal_obligation_id','')='' THEN RAISE EXCEPTION 'renewal evidence missing' USING ERRCODE='HD409'; END IF;
   deadline:=failure+interval '7 days';
   UPDATE handdraw.retention_episodes SET status='canceled' WHERE workspace_id=r.workspace_id AND status='retaining' AND access_ended_at<>deadline;
   UPDATE handdraw.subscriptions SET status='grace_period',renewal_failure_at=failure,renewal_obligation_id=coalesce(renewal_obligation_id,p->>'renewal_obligation_id'),grace_ends_at=deadline,access_expires_at=deadline,revision=revision+1,updated_at=clock_timestamp(),reconciled_at=clock_timestamp() WHERE workspace_id=r.workspace_id AND (renewal_failure_at IS NULL OR status<>'grace_period');
  ELSE
   deadline:=coalesce(s.grace_ends_at,s.paid_through_at,s.trial_ends_at,s.access_expires_at);
   UPDATE handdraw.subscriptions SET status=CASE WHEN grace_ends_at>clock_timestamp() THEN 'grace_period' WHEN deadline>clock_timestamp() THEN status ELSE 'ended' END,access_expires_at=deadline,revision=revision+1,updated_at=clock_timestamp(),reconciled_at=clock_timestamp() WHERE workspace_id=r.workspace_id AND (access_expires_at IS DISTINCT FROM deadline OR status<>'ended' AND deadline<=clock_timestamp());
  END IF;
  RETURN;
 END IF;
 IF previous.version=ver AND r.status='applied' THEN RETURN; END IF;
 IF r.kind='seat_change' AND r.target_seats<r.source_seats AND r.status<>'applied' THEN
  IF r.effective_at>clock_timestamp() OR state<>'active' THEN RETURN; END IF;
  UPDATE handdraw.workspace_members SET role='viewer',revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id AND role='editor' AND user_id IN (SELECT jsonb_array_elements_text(r.member_changes->'demote_user_ids'));
  UPDATE handdraw.invitations SET status='revoked',updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id AND status='pending' AND id IN (SELECT jsonb_array_elements_text(r.member_changes->'revoke_invitation_ids'));
 END IF;
 IF r.kind='plan_downgrade' AND r.status<>'applied' THEN
  DELETE FROM handdraw.board_grants g WHERE g.workspace_id=r.workspace_id AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.member_changes->'revoke_board_grants') x WHERE x->>'board_id'=g.board_id AND x->>'user_id'=g.user_id);
  UPDATE handdraw.invitations SET status='revoked',updated_at=clock_timestamp() WHERE id IN (SELECT jsonb_array_elements_text(r.member_changes->'revoke_invitation_ids')) AND workspace_id=r.workspace_id AND status='pending';
  DELETE FROM handdraw.workspace_members WHERE workspace_id=r.workspace_id AND user_id IN (SELECT jsonb_array_elements_text(r.member_changes->'remove_user_ids')) AND role<>'owner';
 END IF;
 UPDATE handdraw.workspaces SET kind=CASE WHEN r.target_plan='cloud' THEN 'personal' ELSE 'team' END,access_revision=access_revision+1,updated_at=clock_timestamp() WHERE id=r.workspace_id;
 INSERT INTO handdraw.subscriptions(workspace_id,provider,provider_customer_id,provider_subscription_id,provider_status,plan,billing_interval,status,paid_seats,trial_ends_at,paid_through_at,last_successful_payment_at,access_expires_at,cancel_at_period_end,reconciled_at,current_period_start,current_period_end)
 VALUES(r.workspace_id,'polar',p->>'customer_id',sid,state,r.target_plan,r.billing_interval,state,r.target_seats,CASE WHEN state='trialing' THEN deadline END,CASE WHEN state='active' THEN deadline END,CASE WHEN state='active' THEN (SELECT max(paid_at) FROM handdraw.payment_orders WHERE provider_subscription_id=sid) END,deadline,coalesce((p->>'cancel_at_period_end')::boolean,false),clock_timestamp(),(SELECT min(service_from) FROM handdraw.payment_orders WHERE provider_subscription_id=sid AND service_until=deadline),deadline)
 ON CONFLICT(workspace_id) DO UPDATE SET provider='polar',provider_customer_id=p->>'customer_id',provider_subscription_id=sid,provider_status=state,plan=r.target_plan,billing_interval=r.billing_interval,status=state,paid_seats=r.target_seats,trial_ends_at=excluded.trial_ends_at,paid_through_at=excluded.paid_through_at,current_period_start=excluded.current_period_start,current_period_end=deadline,last_successful_payment_at=excluded.last_successful_payment_at,access_expires_at=deadline,renewal_obligation_id=NULL,renewal_failure_at=NULL,grace_ends_at=NULL,cancel_at_period_end=excluded.cancel_at_period_end,revision=handdraw.subscriptions.revision+1,updated_at=clock_timestamp(),reconciled_at=clock_timestamp();
 UPDATE handdraw.billing_change_intents SET status='applied',phase='complete' WHERE id=i;
 UPDATE handdraw.retention_episodes SET status='canceled' WHERE workspace_id=r.workspace_id AND status='retaining';
END; $$;

ALTER FUNCTION handdraw.polar_billing_prepare(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.polar_portal_customer(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.apply_polar_billing(text,text,jsonb) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.polar_portal_customer(text),handdraw.apply_polar_billing(text,text,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.polar_portal_customer(text) TO handdraw_request;
GRANT EXECUTE ON FUNCTION handdraw.apply_polar_billing(text,text,jsonb) TO handdraw_billing_runtime;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;

COMMIT;
