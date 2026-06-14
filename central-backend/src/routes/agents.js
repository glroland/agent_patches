import { Router } from 'express';
import * as agentsController from '../controllers/agentsController.js';

const router = Router();

router.get('/', agentsController.listAgents);
router.get('/:id', agentsController.getAgent);
router.get('/:id/activity', agentsController.getAgentActivity);
router.post('/:id/messages', agentsController.sendAgentMessage);

export default router;
